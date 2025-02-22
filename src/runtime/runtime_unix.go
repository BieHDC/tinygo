//go:build (darwin || (linux && !baremetal && !wasip1 && !wasm_unknown && !wasip2)) && !nintendoswitch

package runtime

import (
	"math/bits"
	"sync/atomic"
	"unsafe"
)

//export write
func libc_write(fd int32, buf unsafe.Pointer, count uint) int

//export usleep
func usleep(usec uint) int

//export pause
func pause() int32

// void *mmap(void *addr, size_t length, int prot, int flags, int fd, off_t offset);
// Note: off_t is defined as int64 because:
//   - musl (used on Linux) always defines it as int64
//   - darwin is practically always 64-bit anyway
//
//export mmap
func mmap(addr unsafe.Pointer, length uintptr, prot, flags, fd int, offset int64) unsafe.Pointer

//export abort
func abort()

//export exit
func exit(code int)

//export raise
func raise(sig int32)

//export clock_gettime
func libc_clock_gettime(clk_id int32, ts *timespec)

//export __clock_gettime64
func libc_clock_gettime64(clk_id int32, ts *timespec)

// Portable (64-bit) variant of clock_gettime.
func clock_gettime(clk_id int32, ts *timespec) {
	if TargetBits == 32 {
		// This is a 32-bit architecture (386, arm, etc).
		// We would like to use the 64-bit version of this function so that
		// binaries will continue to run after Y2038.
		// For more information:
		//   - https://musl.libc.org/time64.html
		//   - https://sourceware.org/glibc/wiki/Y2038ProofnessDesign
		libc_clock_gettime64(clk_id, ts)
	} else {
		// This is a 64-bit architecture (amd64, arm64, etc).
		// Use the regular variant, because it already fixes the Y2038 problem
		// by using 64-bit integer types.
		libc_clock_gettime(clk_id, ts)
	}
}

type timeUnit int64

// Note: tv_sec and tv_nsec normally vary in size by platform. However, we're
// using the time64 variant (see clock_gettime above), so the formats are the
// same between 32-bit and 64-bit architectures.
// There is one issue though: on big-endian systems, tv_nsec would be incorrect.
// But we don't support big-endian systems yet (as of 2021) so this is fine.
type timespec struct {
	tv_sec  int64 // time_t with time64 support (always 64-bit)
	tv_nsec int64 // unsigned 64-bit integer on all time64 platforms
}

var stackTop uintptr

// Entry point for Go. Initialize all packages and call main.main().
//
//export main
func main(argc int32, argv *unsafe.Pointer) int {
	preinit()

	// Store argc and argv for later use.
	main_argc = argc
	main_argv = argv

	// Register some fatal signals, so that we can print slightly better error
	// messages.
	tinygo_register_fatal_signals()

	// Obtain the initial stack pointer right before calling the run() function.
	// The run function has been moved to a separate (non-inlined) function so
	// that the correct stack pointer is read.
	stackTop = getCurrentStackPointer()
	runMain()

	// For libc compatibility.
	return 0
}

var (
	main_argc int32
	main_argv *unsafe.Pointer
	args      []string
)

//go:linkname os_runtime_args os.runtime_args
func os_runtime_args() []string {
	if args == nil {
		// Make args slice big enough so that it can store all command line
		// arguments.
		args = make([]string, main_argc)

		// Initialize command line parameters.
		argv := main_argv
		for i := 0; i < int(main_argc); i++ {
			// Convert the C string to a Go string.
			length := strlen(*argv)
			arg := (*_string)(unsafe.Pointer(&args[i]))
			arg.length = length
			arg.ptr = (*byte)(*argv)
			// This is the Go equivalent of "argv++" in C.
			argv = (*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(argv), unsafe.Sizeof(argv)))
		}
	}
	return args
}

// Must be a separate function to get the correct stack pointer.
//
//go:noinline
func runMain() {
	run()
}

//export tinygo_register_fatal_signals
func tinygo_register_fatal_signals()

// Print fatal errors when they happen, including the instruction location.
// With the particular formatting below, `tinygo run` can extract the location
// where the signal happened and try to show the source location based on DWARF
// information.
//
//export tinygo_handle_fatal_signal
func tinygo_handle_fatal_signal(sig int32, addr uintptr) {
	if panicStrategy() == panicStrategyTrap {
		trap()
	}

	// Print signal including the faulting instruction.
	if addr != 0 {
		printstring("panic: runtime error at ")
		printptr(addr)
	} else {
		printstring("panic: runtime error")
	}
	printstring(": caught signal ")
	switch sig {
	case sig_SIGBUS:
		println("SIGBUS")
	case sig_SIGILL:
		println("SIGILL")
	case sig_SIGSEGV:
		println("SIGSEGV")
	default:
		println(sig)
	}

	// TODO: it might be interesting to also print the invalid address for
	// SIGSEGV and SIGBUS.

	// Do *not* abort here, instead raise the same signal again. The signal is
	// registered with SA_RESETHAND which means it executes only once. So when
	// we raise the signal again below, the signal isn't handled specially but
	// is handled in the default way (probably exiting the process, maybe with a
	// core dump).
	raise(sig)
}

//go:extern environ
var environ *unsafe.Pointer

//go:linkname syscall_runtime_envs syscall.runtime_envs
func syscall_runtime_envs() []string {
	// Count how many environment variables there are.
	env := environ
	numEnvs := 0
	for *env != nil {
		numEnvs++
		env = (*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(env), unsafe.Sizeof(environ)))
	}

	// Create a string slice of all environment variables.
	// This requires just a single heap allocation.
	env = environ
	envs := make([]string, 0, numEnvs)
	for *env != nil {
		ptr := *env
		length := strlen(ptr)
		s := _string{
			ptr:    (*byte)(ptr),
			length: length,
		}
		envs = append(envs, *(*string)(unsafe.Pointer(&s)))
		env = (*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(env), unsafe.Sizeof(environ)))
	}

	return envs
}

func putchar(c byte) {
	buf := [1]byte{c}
	libc_write(1, unsafe.Pointer(&buf[0]), 1)
}

func ticksToNanoseconds(ticks timeUnit) int64 {
	// The OS API works in nanoseconds so no conversion necessary.
	return int64(ticks)
}

func nanosecondsToTicks(ns int64) timeUnit {
	// The OS API works in nanoseconds so no conversion necessary.
	return timeUnit(ns)
}

func sleepTicks(d timeUnit) {
	// When there are no signal handlers present, we can simply go to sleep.
	if !hasSignals {
		// timeUnit is in nanoseconds, so need to convert to microseconds here.
		usleep(uint(d) / 1000)
		return
	}

	if GOOS == "darwin" {
		// Check for incoming signals.
		if checkSignals() {
			// Received a signal, so there's probably at least one goroutine
			// that's runnable again.
			return
		}

		// WARNING: there is a race condition here. If a signal arrives between
		// checkSignals() and usleep(), the usleep() call will not exit early so
		// the signal is delayed until usleep finishes or another signal
		// arrives.
		// There doesn't appear to be a simple way to fix this on MacOS.

		// timeUnit is in nanoseconds, so need to convert to microseconds here.
		result := usleep(uint(d) / 1000)
		if result != 0 {
			checkSignals()
		}
	} else {
		// Linux (and various other POSIX systems) implement sigtimedwait so we
		// can do this in a non-racy way.
		tinygo_wfi_mask(activeSignals)
		if checkSignals() {
			tinygo_wfi_unmask()
			return
		}
		signal := tinygo_wfi_sleep(activeSignals, uint64(d))
		if signal >= 0 {
			tinygo_signal_handler(signal)
			checkSignals()
		}
		tinygo_wfi_unmask()
	}
}

func getTime(clock int32) uint64 {
	ts := timespec{}
	clock_gettime(clock, &ts)
	return uint64(ts.tv_sec)*1000*1000*1000 + uint64(ts.tv_nsec)
}

// Return monotonic time in nanoseconds.
func monotime() uint64 {
	return getTime(clock_MONOTONIC_RAW)
}

func ticks() timeUnit {
	return timeUnit(monotime())
}

//go:linkname now time.now
func now() (sec int64, nsec int32, mono int64) {
	ts := timespec{}
	clock_gettime(clock_REALTIME, &ts)
	sec = int64(ts.tv_sec)
	nsec = int32(ts.tv_nsec)
	mono = nanotime()
	return
}

//go:linkname syscall_Exit syscall.Exit
func syscall_Exit(code int) {
	exit(code)
}

// TinyGo does not yet support any form of parallelism on an OS, so these can be
// left empty.

//go:linkname procPin sync/atomic.runtime_procPin
func procPin() {
}

//go:linkname procUnpin sync/atomic.runtime_procUnpin
func procUnpin() {
}

var heapSize uintptr = 128 * 1024 // small amount to start
var heapMaxSize uintptr

var heapStart, heapEnd uintptr

func preinit() {
	// Allocate a large chunk of virtual memory. Because it is virtual, it won't
	// really be allocated in RAM. Memory will only be allocated when it is
	// first touched.
	heapMaxSize = 1 * 1024 * 1024 * 1024 // 1GB for the entire heap
	for {
		addr := mmap(nil, heapMaxSize, flag_PROT_READ|flag_PROT_WRITE, flag_MAP_PRIVATE|flag_MAP_ANONYMOUS, -1, 0)
		if addr == unsafe.Pointer(^uintptr(0)) {
			// Heap was too big to be mapped by mmap. Reduce the maximum size.
			// We might want to make this a bit smarter than simply halving the
			// heap size.
			// This can happen on 32-bit systems.
			heapMaxSize /= 2
			if heapMaxSize < 4096 {
				runtimePanic("cannot allocate heap memory")
			}
			continue
		}
		heapStart = uintptr(addr)
		heapEnd = heapStart + heapSize
		break
	}
}

// growHeap tries to grow the heap size. It returns true if it succeeds, false
// otherwise.
func growHeap() bool {
	if heapSize == heapMaxSize {
		// Already at the max. If we run out of memory, we should consider
		// increasing heapMaxSize on 64-bit systems.
		return false
	}
	// Grow the heap size used by the program.
	heapSize = (heapSize * 4 / 3) &^ 4095 // grow by around 33%
	if heapSize > heapMaxSize {
		heapSize = heapMaxSize
	}
	setHeapEnd(heapStart + heapSize)
	return true
}

func init() {
	// Set up a channel to receive signals into.
	signalChan = make(chan uint32, 1)
}

var signalChan chan uint32

// Indicate whether signals have been registered.
var hasSignals bool

// Mask of signals that have been received. The signal handler atomically ORs
// signals into this value.
var receivedSignals uint32

var activeSignals uint32

//go:linkname signal_enable os/signal.signal_enable
func signal_enable(s uint32) {
	if s >= 32 {
		// TODO: to support higher signal numbers, we need to turn
		// receivedSignals into a uint32 array.
		runtimePanicAt(returnAddress(0), "unsupported signal number")
	}
	hasSignals = true
	activeSignals |= 1 << s
	// It's easier to implement this function in C.
	tinygo_signal_enable(s)
}

//go:linkname signal_ignore os/signal.signal_ignore
func signal_ignore(s uint32) {
	if s >= 32 {
		// TODO: to support higher signal numbers, we need to turn
		// receivedSignals into a uint32 array.
		runtimePanicAt(returnAddress(0), "unsupported signal number")
	}
	activeSignals &^= 1 << s
	tinygo_signal_ignore(s)
}

//go:linkname signal_disable os/signal.signal_disable
func signal_disable(s uint32) {
	if s >= 32 {
		// TODO: to support higher signal numbers, we need to turn
		// receivedSignals into a uint32 array.
		runtimePanicAt(returnAddress(0), "unsupported signal number")
	}
	activeSignals &^= 1 << s
	tinygo_signal_disable(s)
}

//go:linkname signal_waitUntilIdle os/signal.signalWaitUntilIdle
func signal_waitUntilIdle() {
	// Make sure all signals are sent on the channel.
	for atomic.LoadUint32(&receivedSignals) != 0 {
		checkSignals()
		Gosched()
	}

	// Make sure all signals are processed.
	for len(signalChan) != 0 {
		Gosched()
	}
}

//export tinygo_signal_enable
func tinygo_signal_enable(s uint32)

//export tinygo_signal_ignore
func tinygo_signal_ignore(s uint32)

//export tinygo_signal_disable
func tinygo_signal_disable(s uint32)

// void tinygo_signal_handler(int sig);
//
//export tinygo_signal_handler
func tinygo_signal_handler(s int32) {
	// This loop is essentially the atomic equivalent of the following:
	//
	//   receivedSignals |= 1 << s
	//
	// TODO: use atomic.Uint32.And once we drop support for Go 1.22 instead of
	// this loop.
	for {
		mask := uint32(1) << uint32(s)
		val := atomic.LoadUint32(&receivedSignals)
		swapped := atomic.CompareAndSwapUint32(&receivedSignals, val, val|mask)
		if swapped {
			break
		}
	}
}

//go:linkname signal_recv os/signal.signal_recv
func signal_recv() uint32 {
	// Function called from os/signal to get the next received signal.
	val := <-signalChan
	checkSignals()
	return val
}

// Atomically find a signal that previously occured and send it into the
// signalChan channel. Return true if at least one signal was delivered this
// way, false otherwise.
func checkSignals() bool {
	gotSignals := false
	for {
		// Extract the lowest numbered signal number from receivedSignals.
		val := atomic.LoadUint32(&receivedSignals)
		if val == 0 {
			// There is no signal ready to be received by the program (common
			// case).
			return gotSignals
		}
		num := uint32(bits.TrailingZeros32(val))

		// Do a non-blocking send on signalChan.
		select {
		case signalChan <- num:
			// There was room free in the channel, so remove the signal number
			// from the receivedSignals mask.
			gotSignals = true
		default:
			// Could not send the signal number on the channel. This means
			// there's still a signal pending. In that case, let it be received
			// at which point checkSignals is called again to put the next one
			// in the channel buffer.
			return gotSignals
		}

		// Atomically clear the signal number from receivedSignals.
		// TODO: use atomic.Uint32.Or once we drop support for Go 1.22 instead
		// of this loop.
		for {
			newVal := val &^ (1 << num)
			swapped := atomic.CompareAndSwapUint32(&receivedSignals, val, newVal)
			if swapped {
				break
			}
			val = atomic.LoadUint32(&receivedSignals)
		}
	}
}

//export tinygo_wfi_mask
func tinygo_wfi_mask(active uint32)

//export tinygo_wfi_sleep
func tinygo_wfi_sleep(active uint32, timeout uint64) int32

//export tinygo_wfi_wait
func tinygo_wfi_wait(active uint32) int32

//export tinygo_wfi_unmask
func tinygo_wfi_unmask()

func waitForEvents() {
	if hasSignals {
		// We could have used pause() here, but that function is impossible to
		// use in a race-free way:
		// https://www.cipht.net/2023/11/30/perils-of-pause.html
		// Therefore we need something better.
		// Note: this is unsafe with multithreading, because sigprocmask is only
		// defined for single-threaded applictions.
		tinygo_wfi_mask(activeSignals)
		if checkSignals() {
			tinygo_wfi_unmask()
			return
		}
		signal := tinygo_wfi_wait(activeSignals)
		tinygo_signal_handler(signal)
		checkSignals()
		tinygo_wfi_unmask()
	} else {
		// The program doesn't use signals, so this is a deadlock.
		runtimePanic("deadlocked: no event source")
	}
}
