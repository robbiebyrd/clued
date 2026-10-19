// Every form of `use`, `extern crate` and `mod` declarations.
extern crate alloc;
extern crate serde_json as json;

mod parser;
#[cfg(test)]
mod tests;
pub mod api;

use std::io;
use std::collections::HashMap;
use std::sync::{Arc, Mutex, mpsc::Sender as Tx};
use std::fmt::{self, Display};
use crate::config::Config as Settings;
use super::*;
use self::parser::{ast::{Expr, Stmt}, lexer::*};
pub use api::Client;
use ::serde::Deserialize;
use tokio;
use log as logging;

/// Reads the settings.
pub fn load(path: &str) -> io::Result<Settings> {
    let text = std::fs::read_to_string(path)?;
    let config: Settings = json::from_str(&text).map_err(io::Error::other)?;
    Ok(config)
}
