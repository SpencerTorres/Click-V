use core::panic::PanicInfo;
use crate::syscall::{Syscall, syscall1, syscall2, syscall3, syscall5};

pub fn print(msg: &[u8]) {
    unsafe {
        syscall2(Syscall::Print, msg.as_ptr() as isize, msg.len() as isize);
    }
}

pub fn open(path_name: &[u8], flags: isize) -> isize {
    unsafe {
        syscall3(Syscall::Open, path_name.as_ptr() as isize, path_name.len() as isize, flags)
    }
}

pub fn socket(address: &[u8]) -> isize {
    unsafe {
        syscall2(Syscall::Socket, address.as_ptr() as isize, address.len() as isize)
    }
}

pub fn close(fd: isize) -> isize {
    unsafe {
        syscall1(Syscall::Close, fd)
    }
}

pub fn seek(fd: isize, offset: isize, whence: isize) -> isize {
    unsafe {
        syscall3(Syscall::Seek, fd, offset, whence)
    }
}

pub fn read(fd: isize, buf: &mut [u8]) -> isize {
    unsafe {
        syscall3(Syscall::Read, fd, buf.as_ptr() as isize, buf.len() as isize)
    }
}

pub fn write_u8_slice(fd: isize, buf: &[u8]) -> isize {
    unsafe {
        syscall3(Syscall::Write, fd, buf.as_ptr() as isize, buf.len() as isize)
    }
}

pub fn write_ptr(fd: isize, ptr: *const u8, count: usize) -> isize {
    unsafe {
        syscall3(Syscall::Write, fd, ptr as isize, count as isize)
    }
}

#[panic_handler]
fn panic(_info: &PanicInfo) -> ! {
    loop {}
}
