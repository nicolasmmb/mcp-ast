use std::fmt;

mod utils {}

struct Point {
    x: i32,
}

enum Color {
    Red,
    Blue,
}

trait Drawable {
    fn draw(&self);
}

impl Drawable for Point {
    fn draw(&self) {}
}

type ID = i32;

fn helper() -> i32 {
    let x = 1;
    x
}

fn main() {
    let _ = helper();
    helper();
}
