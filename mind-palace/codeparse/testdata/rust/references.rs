use std::fmt;

use crate::shapes::{Circle, Shape};

/// Draws shapes onto a canvas.
pub trait Draw: fmt::Debug + Shape {
    fn draw(&self, canvas: &mut Canvas);
}

/// A canvas of pixels.
pub struct Canvas {
    pixels: Vec<Pixel>,
    size: geometry::Size,
}

impl Draw for Circle {
    fn draw(&self, canvas: &mut Canvas) {
        let center = self.center();
        canvas.plot(center, Color::RED);
        render::<Circle>(self, canvas);
        geometry::arc(center, self.radius);
        trace!("drew {:?}", self);
        log::debug!("circle at {:?}", center);
        println!("done");
    }
}

impl<T: Shape> From<T> for Canvas {
    fn from(shape: T) -> Self {
        let mut c = Canvas::with_size(shape.bounds());
        c.fill(shape.color().into());
        c
    }
}

impl fmt::Display for Canvas {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let n = self.pixels.iter().filter(|p| p.lit()).count();
        write!(f, "{}", n)
    }
}

/// Renders any shape.
pub fn render<S: Shape + ?Sized>(shape: &S, canvas: &mut Canvas) -> Option<Pixel> {
    let first = canvas.pixels.first().copied();
    shape.paint(canvas);
    Some(first?)
}
