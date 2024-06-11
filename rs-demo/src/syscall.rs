use core::arch::asm;

pub enum Syscall {
    Print,
    Draw,

    // ClickOS
    Reset,
    Open,
    Close,
    Seek,
    Read,
    Write,
    Socket,
}

impl Syscall {
    pub fn to_isize(&self) -> isize {
        match *self {
            Syscall::Print => 1,
            Syscall::Draw => 2,

            Syscall::Reset => 0,
            Syscall::Open => 10,
            Syscall::Close => 11,
            Syscall::Seek => 12,
            Syscall::Read => 13,
            Syscall::Write => 14,
            Syscall::Socket => 15,
        }
    }
}

// These asm blocks were taken from here since I couldn't get it to build with the syscalls crate
// https://github.com/jasonwhite/syscalls/blob/main/src/syscall/riscv32.rs
// Also changed a lot of them from usize to isize

#[inline]
pub unsafe fn syscall0(n: Syscall) -> isize {
    let mut ret: isize;
    asm!(
    "ecall",
    in("a7") n.to_isize(),
    out("a0") ret,
    options(nostack, preserves_flags)
    );
    ret
}

#[inline]
pub unsafe fn syscall1(n: Syscall, arg1: isize) -> isize {
    let mut ret: isize;
    asm!(
    "ecall",
    in("a7") n.to_isize(),
    inlateout("a0") arg1 => ret,
    options(nostack, preserves_flags)
    );
    ret
}

#[inline]
pub unsafe fn syscall2(n: Syscall, arg1: isize, arg2: isize) -> isize {
    let mut ret: isize;
    asm!(
    "ecall",
    in("a7") n.to_isize(),
    inlateout("a0") arg1 => ret,
    in("a1") arg2,
    options(nostack, preserves_flags)
    );
    ret
}

#[inline]
pub unsafe fn syscall3(n: Syscall, arg1: isize, arg2: isize, arg3: isize) -> isize {
    let mut ret: isize;
    asm!(
    "ecall",
    in("a7") n.to_isize(),
    inlateout("a0") arg1 => ret,
    in("a1") arg2,
    in("a2") arg3,
    options(nostack, preserves_flags)
    );
    ret
}

#[inline]
pub unsafe fn syscall4(n: Syscall, arg1: isize, arg2: isize, arg3: isize, arg4: isize) -> isize {
    let mut ret: isize;
    asm!(
    "ecall",
    in("a7") n.to_isize(),
    inlateout("a0") arg1 => ret,
    in("a1") arg2,
    in("a2") arg3,
    in("a3") arg4,
    options(nostack, preserves_flags)
    );
    ret
}

#[inline]
pub unsafe fn syscall5(n: Syscall, arg1: isize, arg2: isize, arg3: isize, arg4: isize, arg5: isize) -> isize {
    let mut ret: isize;
    asm!(
    "ecall",
    in("a7") n.to_isize(),
    inlateout("a0") arg1 => ret,
    in("a1") arg2,
    in("a2") arg3,
    in("a3") arg4,
    in("a4") arg5,
    options(nostack, preserves_flags)
    );
    ret
}
