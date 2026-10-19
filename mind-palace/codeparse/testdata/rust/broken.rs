//! A file with a syntax error in the middle.

/// Parsed despite the error below.
pub fn good() -> u32 {
    1
}

pub fn bad(x: u32 -> u32 {
    x +
}

/// After the error.
pub struct After {
    pub field: u32,
}

impl After {
    pub fn get(&self) -> u32 {
        good()
    }
}
