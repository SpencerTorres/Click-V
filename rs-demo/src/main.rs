#![no_std]
#![no_main]

use core::arch::global_asm;
use crate::syscall::{Syscall};
use crate::system::{close, open, print, read, write_u8_slice, write_ptr, socket};

mod screen;
mod syscall;
mod system;

global_asm!(include_str!("../start.s")); // init stack pointer

#[no_mangle]
pub extern "C" fn main() -> ! {
    print(b"Running syscalls.\n");

    let writing_file = open(b"./file.txt", 0);
    if writing_file < 0 {
        print(b"open write file failed\n");
    }

    let n = write_u8_slice(writing_file, b"ClickHouse!");
    if n < 0 {
        print(b"write to file failed\n");
    }

    let c = close(writing_file);
    if c < 0 {
        print(b"close write file failed\n");
    }

    let socket = socket(b"localhost:9008");
    if socket < 0 {
        print(b"failed to bind socket\n");
    }

    let n = write_u8_slice(socket, b"128	[]");
    if n < 0 {
        print(b"write to socket failed\n");
    }

    let mut buf = [0u8; 32];
    let n = read(socket, &mut buf);
    if n == -64 {
        print(b"no data in socket, try again\n");
    } else if n < 0 {
        print(b"read from socket failed\n");
    }

    print(b"read from socket:\n");
    print(&buf[..n as usize]);
    print(b"\n");

    let c = close(socket);
    if c < 0 {
        print(b"close socket failed\n");
    }

    let reading_file = open(b"./file.txt", 0);
    if reading_file < 0 {
        print(b"open read file failed\n");
    }

    let n = read(reading_file, &mut buf);
    if n < 0 {
        print(b"read from file failed\n");
    }

    let c = close(reading_file);
    if c < 0 {
        print(b"close read file failed\n");
    }

    print(b"read from file:\n");
    print(&buf[..n as usize]);

    let mut i = 0;
    while i < screen::SIZE {
        screen::set_cell_by_index(i, screen::Color::from_index(i as u8 % screen::NUM_COLORS));
        screen::draw_screen();
        i += 1;
    }
    print(b"Updated pixels.\n");

    let image_file = open(b"./image.bin", 0);
    if image_file < 0 {
        print(b"open write image file failed\n");
    }

    let n = write_ptr(image_file, screen::START_ADDR, screen::SIZE as usize);
    if n < 0 {
        print(b"write to image file failed\n");
    }

    let c = close(image_file);
    if c < 0 {
        print(b"close write file failed\n");
    }

    loop {}
}
