.section .text.start
.global _start

_start:
    la sp, __stack_end
    j main
