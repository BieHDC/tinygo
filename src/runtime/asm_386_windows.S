.section .text.tinygo_scanCurrentStack,"ax"
.global tinygo_scanCurrentStack
.global _tinygo_scanCurrentStack
_tinygo_scanCurrentStack:
    // Sources:

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
_tinygo_longjmp:
    // Note: the code we jump to assumes eax is set to a non-zero value if we
    // jump from here.
    movl 4(%esp), %eax
    movl 0(%eax), %esp // jumpSP
    movl 4(%eax), %eax // jumpPC (stash in volatile register)
    jmpl *%eax

    
/*
https://github.com/reactos/reactos/blob/bbdcc14b1c2078c98dbd02cc498423c081e133fd/ntoskrnl/ke/i386/ctxswitch.S#L80
PUBLIC @KiSwapContext@8
@KiSwapContext@8:
    // Save 4 registers
    sub esp, 4 * 4

    // Save all the non-volatile ones 
    mov [esp+12], ebx
    mov [esp+8], esi
    mov [esp+4], edi
    mov [esp+0], ebp

    // Combine current thread and the wait IRQL in edx
    or dl, cl //???

    // Do the swap with the registers correctly setup
    call @KiSwapContextInternal@0

    // Restore the registers
    mov ebp, [esp+0]
    mov edi, [esp+4]
    mov esi, [esp+8]
    mov ebx, [esp+12]

    // Clean stack
    add esp, 4 * 4
    ret
*/
