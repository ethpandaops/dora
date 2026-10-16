//! C-ABI wasm exports around the ere zkVM verifiers.
//!
//! The host writes the program verification key and the proof into the module
//! memory (`ere_alloc`) and calls `ere_verify`; on success the public values the
//! proof commits to are left in the buffer `ere_out_ptr` points to.

use ere_verifier_core::{codec::Decode, zkVMVerifier};

/// Proof system identifiers of `ere_verify`.
pub const KIND_SP1: u32 = 1;
pub const KIND_ZISK: u32 = 2;
pub const KIND_OPENVM: u32 = 3;

/// Result codes of `ere_verify` (a non-negative result is the public values length).
pub const ERR_PROGRAM_VK: i32 = -1;
pub const ERR_PROOF_DECODE: i32 = -2;
pub const ERR_VERIFICATION: i32 = -3;
pub const ERR_KIND: i32 = -9;

const OUT_SIZE: usize = 4096;
static mut OUT: [u8; OUT_SIZE] = [0; OUT_SIZE];

#[unsafe(no_mangle)]
pub extern "C" fn ere_alloc(len: usize) -> *mut u8 {
    let mut buf = Vec::<u8>::with_capacity(len);
    let ptr = buf.as_mut_ptr();
    std::mem::forget(buf);
    ptr
}

#[unsafe(no_mangle)]
pub extern "C" fn ere_out_ptr() -> *const u8 {
    std::ptr::addr_of!(OUT) as *const u8
}

#[unsafe(no_mangle)]
pub extern "C" fn ere_verify(kind: u32, vk_ptr: *const u8, vk_len: usize, proof_ptr: *const u8, proof_len: usize) -> i32 {
    let vk = unsafe { std::slice::from_raw_parts(vk_ptr, vk_len) };
    let proof = unsafe { std::slice::from_raw_parts(proof_ptr, proof_len) };

    let result = match kind {
        KIND_SP1 => verify::<ere_verifier_sp1::SP1Verifier, _>(vk, proof, ere_verifier_sp1::SP1Verifier::new),
        KIND_ZISK => verify::<ere_verifier_zisk::ZiskVerifier, _>(vk, proof, ere_verifier_zisk::ZiskVerifier::new),
        KIND_OPENVM => verify::<ere_verifier_openvm::OpenVMVerifier, _>(vk, proof, ere_verifier_openvm::OpenVMVerifier::new),
        _ => return ERR_KIND,
    };

    match result {
        Ok(public_values) => {
            let len = public_values.len().min(OUT_SIZE);
            unsafe { std::ptr::copy_nonoverlapping(public_values.as_ptr(), std::ptr::addr_of_mut!(OUT) as *mut u8, len) };
            len as i32
        }
        Err(code) => code,
    }
}

fn verify<V, F>(vk: &[u8], proof: &[u8], new: F) -> Result<Vec<u8>, i32>
where
    V: zkVMVerifier,
    F: FnOnce(V::ProgramVk) -> V,
{
    let vk = V::ProgramVk::decode_from_slice(vk).map_err(|_| ERR_PROGRAM_VK)?;
    let proof = V::Proof::decode_from_slice(proof).map_err(|_| ERR_PROOF_DECODE)?;
    new(vk).verify(&proof).map(Into::into).map_err(|_| ERR_VERIFICATION)
}

// getrandom backends that refuse: nothing in the verify path asks for randomness.
fn no_randomness(_: &mut [u8]) -> Result<(), getrandom02::Error> {
    Err(getrandom02::Error::UNSUPPORTED)
}
getrandom02::register_custom_getrandom!(no_randomness);

#[unsafe(no_mangle)]
unsafe extern "Rust" fn __getrandom_v03_custom(_dest: *mut u8, _len: usize) -> Result<(), getrandom03::Error> {
    Err(getrandom03::Error::UNSUPPORTED)
}
