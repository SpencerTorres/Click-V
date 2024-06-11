OUTPUT_ARCH(riscv)
ENTRY(_start)

MEMORY
{
  ROM (rx) : ORIGIN = 0x00000000, LENGTH = 2K
  RAM (rwx) : ORIGIN = 0x00000800, LENGTH = 1K
  VRAM (w) : ORIGIN = 0x00000C00, LENGTH = 800
}

SECTIONS
{
  .text : {
    . = ALIGN(4);
    *(.text*)
    *(.rodata*)
    . = ALIGN(4);
  } > ROM

  .data : {
    . = ALIGN(4);
    __data_start = .;
    *(.data*)
    . = ALIGN(4);
    __data_end = .;
  } > RAM AT> ROM

  .bss : {
    . = ALIGN(4);
    __bss_start = .;
    *(.bss*)
    *(COMMON)
    . = ALIGN(4);
    __bss_end = .;
  } > RAM

  .stack (NOLOAD) : {
    . = ALIGN(4);
    __stack_start = .;
    . += 128; /* 128 byte stack */
    . = ALIGN(4);
    __stack_end = .;
  } > RAM

  /DISCARD/ : {
    *(.eh_frame)
  }
}
