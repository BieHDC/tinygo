.section .text.tinygo_scanCurrentStack,"ax"
.global tinygo_scanCurrentStack
.global _tinygo_scanCurrentStack
tinygo_scanCurrentStack:
_tinygo_scanCurrentStack:
    // Sources:
    //   * https://stackoverflow.com/questions/18024672/what-registers-are-preserved-through-a-linux-x86-64-function-call
    //   * https://godbolt.org/z/q7e8dn

    // Save callee-saved registers.
    pushl %ebx
    pushl %esi
    pushl %edi
    pushl %ebp

    // Scan the stack.
    subl $8, %esp // adjust the stack before the call to maintain 16-byte alignment
    pushl %esp
    calll _tinygo_scanstack

    // Restore the stack pointer. Registers do not need to be restored as they
    // were only pushed to be discoverable by the GC.
    addl $28, %esp
    retl


.section .text.tinygo_longjmp,"ax"
.global tinygo_longjmp
.global _tinygo_longjmp
tinygo_longjmp:
_tinygo_longjmp:
    // Note: the code we jump to assumes eax is set to a non-zero value if we
    // jump from here.
    movl 4(%esp), %eax
    movl 0(%eax), %esp // jumpSP
    movl 4(%eax), %eax // jumpPC (stash in volatile register)
    jmpl *%eax
