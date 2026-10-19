//! A small key-value store: the crate root of the golden inputs.

pub mod error;
mod storage;
pub(crate) mod util {
    /// Clamps a capacity to the allowed range.
    pub fn clamp(n: usize) -> usize {
        n.min(MAX_ENTRIES)
    }
}

use std::collections::HashMap;
use std::fmt;

/// The most entries a store holds.
pub const MAX_ENTRIES: usize = 1024;

/// Counts every store ever opened.
static OPENED: AtomicUsize = AtomicUsize::new(0);

/// A key in the store.
pub type Key = String;

/// An entry with its value and version.
#[derive(Debug, Clone, PartialEq)]
#[non_exhaustive]
pub struct Entry<V> {
    /// The stored value.
    pub value: V,
    #[serde(default)]
    pub(crate) version: u64,
    created: Instant,
}

/// A tuple struct.
pub struct Version(pub u64);

/// A unit struct marks an empty store.
struct Empty;

/// What a lookup found.
#[derive(Debug)]
pub enum Lookup<V> {
    /// The key holds a value.
    Found(V),
    /// The key was removed at this version.
    Removed { at: u64, by: Key },
    Missing,
}

/// Raw bytes or a number.
#[repr(C)]
pub union Raw {
    bytes: [u8; 8],
    number: u64,
}

/// Anything that can be stored.
pub trait Storable: fmt::Debug + Clone {
    /// The encoded form.
    type Encoded;

    /// The version this kind starts at.
    const START: u64 = 1;

    /// Encodes the value.
    fn encode(&self) -> Self::Encoded;

    /// Reports the size in bytes.
    fn size(&self) -> usize {
        std::mem::size_of_val(self)
    }
}

/// The store.
#[derive(Default)]
pub struct Store<V: Storable> {
    entries: HashMap<Key, Entry<V>>,
}

impl<V: Storable> Store<V> {
    /// Opens an empty store.
    #[must_use]
    pub fn new() -> Self {
        OPENED.fetch_add(1, Ordering::SeqCst);
        Store { entries: HashMap::new() }
    }

    /// Looks a key up.
    pub fn get(&self, key: &str) -> Lookup<&V> {
        match self.entries.get(key) {
            Some(e) => Lookup::Found(&e.value),
            None => Lookup::Missing,
        }
    }

    fn bump(&mut self) {}
}

impl<V: Storable> fmt::Display for Store<V> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{} entries", self.entries.len())
    }
}

impl Storable for u64 {
    type Encoded = [u8; 8];

    fn encode(&self) -> Self::Encoded {
        self.to_be_bytes()
    }
}

/// Builds a store from pairs.
#[macro_export]
macro_rules! store {
    ($($k:expr => $v:expr),* $(,)?) => {{
        let mut s = $crate::Store::new();
        $( s.insert($k, $v); )*
        s
    }};
}

/// Opens a store with a capacity.
pub async fn open<V>(capacity: usize) -> Result<Store<V>, error::Error>
where
    V: Storable + Send,
{
    let cap = util::clamp(capacity);
    storage::reserve(cap).await?;
    Ok(Store::new())
}

extern "C" {
    /// The C allocator's size of a block.
    fn malloc_usable_size(ptr: *mut u8) -> usize;
}
