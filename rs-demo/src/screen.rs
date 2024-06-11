use crate::syscall::{Syscall, syscall0};

pub const WIDTH: u32 = 40;
pub const HEIGHT: u32 = 20;
pub const SIZE: u32 = WIDTH * HEIGHT;

pub const START_ADDR: *mut u8 = 0x00000C00 as *mut u8;

pub const NUM_COLORS: u8 = 9;
pub enum Color {
    Reset,
    Black,
    Red,
    Green,
    Yellow,
    Blue,
    Magenta,
    Cyan,
    White
}

impl Color {
    pub fn to_ansi(&self) -> u8 {
        match *self {
            Color::Reset => 0,
            Color::Black => 30,
            Color::Red => 31,
            Color::Green => 32,
            Color::Yellow => 33,
            Color::Blue => 34,
            Color::Magenta => 35,
            Color::Cyan => 36,
            Color::White => 37,
        }
    }

    pub fn from_index(value: u8) -> Color {
        match value {
            0 => Color::Reset,
            1 => Color::Black,
            2 => Color::Red,
            3 => Color::Green,
            4 => Color::Yellow,
            5 => Color::Blue,
            6 => Color::Magenta,
            7 => Color::Cyan,
            8 => Color::White,
            _ => Color::Reset
        }
    }
}

pub fn set_cell_by_index(index: u32, color: Color) {
    unsafe {
        START_ADDR.offset(index as isize).write_volatile(color.to_ansi());
    }
}

pub fn set_cell(x: u32, y: u32, color: Color) {
    set_cell_by_index(y * WIDTH + x, color)
}

pub fn draw_screen() {
    unsafe {
        syscall0(Syscall::Draw);
    }
}

