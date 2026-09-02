//! An embedded Rust binding for KVLite's stable C shared-library ABI.
//!
//! `Database::open` dynamically loads a matching `libkvlite` release instead
//! of linking directly to RocksDB. This keeps the Rust crate small and lets
//! applications select a KVLite artifact with `KVLITE_LIBRARY_PATH`.

use std::env;
use std::ffi::{CStr, CString};
use std::fmt::{Display, Formatter};
use std::os::raw::{c_char, c_int, c_void};
use std::path::{Path, PathBuf};
use std::time::Duration;

use libloading::Library;
use serde::de::DeserializeOwned;
use serde::Serialize;

const ABI_VERSION: u32 = 1;
const STATUS_OK: c_int = 0;
const STATUS_NOT_FOUND: c_int = 1;
const STATUS_INVALID_ARGUMENT: c_int = 2;

type AbiVersionFn = unsafe extern "C" fn() -> u32;
type OpenFn = unsafe extern "C" fn(*const c_char, *mut u64, *mut *mut c_char) -> c_int;
type CloseFn = unsafe extern "C" fn(u64, *mut *mut c_char) -> c_int;
type PutFn = unsafe extern "C" fn(u64, *const c_void, usize, *const c_void, usize, i64, *mut *mut c_char) -> c_int;
type GetFn = unsafe extern "C" fn(u64, *const c_void, usize, *mut *mut c_void, *mut usize, *mut *mut c_char) -> c_int;
type DeleteFn = unsafe extern "C" fn(u64, *const c_void, usize, *mut *mut c_char) -> c_int;
type FreeFn = unsafe extern "C" fn(*mut c_void);

/// Errors returned by local KVLite operations.
#[derive(Debug)]
pub enum Error {
    NotFound(String),
    InvalidArgument(String),
    Storage(String),
    Serialization(serde_json::Error),
    NativeLibrary(String),
}

impl Display for Error {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NotFound(message)
            | Self::InvalidArgument(message)
            | Self::Storage(message)
            | Self::NativeLibrary(message) => formatter.write_str(message),
            Self::Serialization(error) => write!(formatter, "KVLite JSON serialization error: {error}"),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Serialization(error) => Some(error),
            _ => None,
        }
    }
}

impl From<serde_json::Error> for Error {
    fn from(error: serde_json::Error) -> Self {
        Self::Serialization(error)
    }
}

/// Convenient result alias for this crate.
pub type Result<T> = std::result::Result<T, Error>;

#[derive(Clone, Copy)]
struct Functions {
    close: CloseFn,
    put: PutFn,
    get: GetFn,
    delete: DeleteFn,
    free: FreeFn,
}

/// A local, SQLite-style KVLite connection.
///
/// The database directory must have only one owner process. Use KVLite's HTTP
/// or Redis server when a database is shared by workers or applications.
pub struct Database {
    // Keep the dynamic library alive longer than every function pointer.
    _library: Library,
    functions: Functions,
    handle: Option<u64>,
}

impl Database {
    /// Open a directory with a matching `libkvlite` shared library.
    ///
    /// This resolves `KVLITE_LIBRARY_PATH`, `KVLITE_HOME`, and the crate's
    /// optional native-artifact directory. Use [`Database::open_with_library`]
    /// to select one explicit native-library file.
    pub fn open(path: impl AsRef<Path>) -> Result<Self> {
        Self::open_inner(path.as_ref(), None)
    }

    /// Open a directory using one explicit `libkvlite` shared-library path.
    pub fn open_with_library(path: impl AsRef<Path>, library_path: impl AsRef<Path>) -> Result<Self> {
        Self::open_inner(path.as_ref(), Some(library_path.as_ref()))
    }

    fn open_inner(path: &Path, library_path: Option<&Path>) -> Result<Self> {
        if path.as_os_str().is_empty() {
            return Err(Error::InvalidArgument("KVLite database path is required.".into()));
        }
        let path = CString::new(path.to_string_lossy().as_bytes())
            .map_err(|_| Error::InvalidArgument("KVLite database path cannot contain a NUL byte.".into()))?;
        let library_path = LibraryFinder::find(library_path)?;
        // SAFETY: Functions are resolved below, retained with the Library, and
        // only invoked with the exact C ABI declared in capi/kvlite.h.
        let library = unsafe { Library::new(&library_path) }
            .map_err(|error| Error::NativeLibrary(format!("Unable to load KVLite native library at {}: {error}", library_path.display())))?;
        let abi_version: AbiVersionFn = unsafe { symbol(&library, b"kvlite_abi_version\0")? };
        // SAFETY: kvlite_abi_version takes no pointers and was resolved above.
        let version = unsafe { abi_version() };
        if version != ABI_VERSION {
            return Err(Error::NativeLibrary(format!(
                "KVLite native ABI mismatch: this Rust crate needs ABI {ABI_VERSION}, got {version}."
            )));
        }
        let open: OpenFn = unsafe { symbol(&library, b"kvlite_open\0")? };
        let functions = Functions {
            close: unsafe { symbol(&library, b"kvlite_close\0")? },
            put: unsafe { symbol(&library, b"kvlite_put\0")? },
            get: unsafe { symbol(&library, b"kvlite_get\0")? },
            delete: unsafe { symbol(&library, b"kvlite_delete\0")? },
            free: unsafe { symbol(&library, b"kvlite_free\0")? },
        };
        let mut handle = 0;
        let mut native_error = std::ptr::null_mut();
        // SAFETY: path is NUL-terminated; all output pointers are valid for the call.
        let status = unsafe { open(path.as_ptr(), &mut handle, &mut native_error) };
        check_status(functions.free, status, native_error)?;

        Ok(Self {
            _library: library,
            functions,
            handle: Some(handle),
        })
    }

    /// Store a JSON-serializable value. `None` means no expiry.
    pub fn put<T: Serialize>(&self, key: impl AsRef<[u8]>, value: &T, ttl: Option<Duration>) -> Result<()> {
        let payload = serde_json::to_vec(value)?;
        self.put_bytes(key, &payload, ttl)
    }

    /// Read and deserialize a JSON value.
    pub fn get<T: DeserializeOwned>(&self, key: impl AsRef<[u8]>) -> Result<T> {
        Ok(serde_json::from_slice(&self.get_bytes(key)?)?)
    }

    /// Store application-owned serialized bytes.
    pub fn put_bytes(&self, key: impl AsRef<[u8]>, value: impl AsRef<[u8]>, ttl: Option<Duration>) -> Result<()> {
        let handle = self.handle()?;
        let key = checked_key(key.as_ref())?;
        let value = value.as_ref();
        let ttl_seconds = ttl_seconds(ttl)?;
        let mut native_error = std::ptr::null_mut();
        // SAFETY: slices remain alive for the full call and their pointer/length
        // pairs follow the C API's byte-buffer contract.
        let status = unsafe {
            (self.functions.put)(
                handle,
                key.as_ptr().cast(),
                key.len(),
                value.as_ptr().cast(),
                value.len(),
                ttl_seconds,
                &mut native_error,
            )
        };
        check_status(self.functions.free, status, native_error)
    }

    /// Read application-owned serialized bytes.
    pub fn get_bytes(&self, key: impl AsRef<[u8]>) -> Result<Vec<u8>> {
        let handle = self.handle()?;
        let key = checked_key(key.as_ref())?;
        let mut value = std::ptr::null_mut();
        let mut length = 0;
        let mut native_error = std::ptr::null_mut();
        // SAFETY: output pointers are valid for the full C call.
        let status = unsafe {
            (self.functions.get)(
                handle,
                key.as_ptr().cast(),
                key.len(),
                &mut value,
                &mut length,
                &mut native_error,
            )
        };
        check_status(self.functions.free, status, native_error)?;
        if value.is_null() && length != 0 {
            return Err(Error::Storage(
                "KVLite native library returned a null value pointer with a non-zero length.".into(),
            ));
        }
        // SAFETY: a successful C API result returns `length` allocated bytes
        // owned by the caller. Copy before freeing them through kvlite_free.
        let result = unsafe {
            let result = if length == 0 {
                Vec::new()
            } else {
                std::slice::from_raw_parts(value.cast::<u8>(), length).to_vec()
            };
            if !value.is_null() {
                (self.functions.free)(value);
            }
            result
        };
        Ok(result)
    }

    /// Delete a key. Deleting an absent key succeeds.
    pub fn delete(&self, key: impl AsRef<[u8]>) -> Result<()> {
        let handle = self.handle()?;
        let key = checked_key(key.as_ref())?;
        let mut native_error = std::ptr::null_mut();
        // SAFETY: key is a valid slice for the call and native_error is writable.
        let status = unsafe {
            (self.functions.delete)(handle, key.as_ptr().cast(), key.len(), &mut native_error)
        };
        check_status(self.functions.free, status, native_error)
    }

    /// Close the native database handle. It is safe to call more than once.
    pub fn close(&mut self) -> Result<()> {
        let Some(handle) = self.handle else {
            return Ok(());
        };
        let mut native_error = std::ptr::null_mut();
        // SAFETY: native_error is writable and handle was returned by kvlite_open.
        let status = unsafe { (self.functions.close)(handle, &mut native_error) };
        check_status(self.functions.free, status, native_error)?;
        self.handle = None;
        Ok(())
    }

    fn handle(&self) -> Result<u64> {
        self.handle.ok_or_else(|| Error::Storage("KVLite database is closed.".into()))
    }
}

impl Drop for Database {
    fn drop(&mut self) {
        let _ = self.close();
    }
}

/// Locate the release shared library used by [`Database::open`].
pub struct LibraryFinder;

impl LibraryFinder {
    pub fn find(requested_path: Option<&Path>) -> Result<PathBuf> {
        let mut candidates = Vec::new();
        if let Some(path) = requested_path {
            candidates.push(path.to_path_buf());
        }
        if let Some(path) = env::var_os("KVLITE_LIBRARY_PATH") {
            candidates.push(PathBuf::from(path));
        }
        if let Some(home) = env::var_os("KVLITE_HOME") {
            candidates.push(PathBuf::from(home).join("lib").join(Self::library_name()));
        }
        let package_root = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        let target = Self::target();
        candidates.push(package_root.join("native").join(&target).join(Self::library_name()));
        candidates.push(package_root.join("../../../dist/dev").join(&target).join("lib").join(Self::library_name()));

        if let Some(path) = candidates.iter().find(|path| path.is_file()) {
            return path.canonicalize().map_err(|error| Error::NativeLibrary(error.to_string()));
        }
        let attempted = candidates
            .iter()
            .map(|path| format!("  - {}", path.display()))
            .collect::<Vec<_>>()
            .join("\n");
        Err(Error::NativeLibrary(format!(
            "KVLite native library was not found. Set KVLITE_LIBRARY_PATH to libkvlite or install a matching KVLite native bundle. Looked in:\n{attempted}"
        )))
    }

    fn library_name() -> &'static str {
        if cfg!(target_os = "windows") {
            "kvlite.dll"
        } else if cfg!(target_os = "macos") {
            "libkvlite.dylib"
        } else {
            "libkvlite.so"
        }
    }

    fn target() -> String {
        let os = if cfg!(target_os = "windows") {
            "windows"
        } else if cfg!(target_os = "macos") {
            "darwin"
        } else {
            "linux"
        };
        let architecture = if cfg!(target_arch = "x86_64") {
            "amd64"
        } else if cfg!(target_arch = "aarch64") {
            "arm64"
        } else {
            env::consts::ARCH
        };
        format!("{os}-{architecture}")
    }
}

unsafe fn symbol<T: Copy>(library: &Library, name: &[u8]) -> Result<T> {
    // SAFETY: caller supplies a NUL-terminated exported C symbol name and T
    // matches the declaration in capi/kvlite.h.
    unsafe { library.get::<T>(name) }
        .map(|symbol| *symbol)
        .map_err(|error| Error::NativeLibrary(format!("libkvlite is missing required symbol: {error}")))
}

fn checked_key(key: &[u8]) -> Result<&[u8]> {
    if key.is_empty() {
        return Err(Error::InvalidArgument("KVLite key is required.".into()));
    }
    Ok(key)
}

fn ttl_seconds(ttl: Option<Duration>) -> Result<i64> {
    let Some(ttl) = ttl else {
        return Ok(0);
    };
    let seconds = ttl
        .as_secs()
        .checked_add(u64::from(ttl.subsec_nanos() != 0))
        .ok_or_else(|| Error::InvalidArgument("KVLite TTL is too large.".into()))?;
    i64::try_from(seconds)
        .map_err(|_| Error::InvalidArgument("KVLite TTL is too large.".into()))
}

fn check_status(free: FreeFn, status: c_int, native_error: *mut c_char) -> Result<()> {
    if status == STATUS_OK {
        if !native_error.is_null() {
            // SAFETY: even an unexpected success-path error pointer follows
            // the ABI's allocator boundary.
            unsafe { free(native_error.cast()) };
        }
        return Ok(());
    }
    let message = if native_error.is_null() {
        "KVLite native operation failed.".to_owned()
    } else {
        // SAFETY: failures return a malloc'd, NUL-terminated message per the ABI.
        let message = unsafe { CStr::from_ptr(native_error).to_string_lossy().into_owned() };
        // SAFETY: the ABI explicitly assigns this allocation to kvlite_free.
        unsafe { free(native_error.cast()) };
        message
    };
    match status {
        STATUS_NOT_FOUND => Err(Error::NotFound(message)),
        STATUS_INVALID_ARGUMENT => Err(Error::InvalidArgument(message)),
        _ => Err(Error::Storage(message)),
    }
}
