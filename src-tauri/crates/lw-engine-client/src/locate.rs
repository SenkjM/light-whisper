// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//! Locating the `lw-engine` binary, mirroring how `engine.exe` is found
//! (bundled resource directory first, development path second).

use std::path::{Path, PathBuf};

/// `lw-engine` or `lw-engine.exe`.
pub fn binary_file_name() -> String {
    format!("lw-engine{}", std::env::consts::EXE_SUFFIX)
}

/// Returns the first existing candidate, in priority order:
/// 1. `override_path` (e.g. the `LW_ENGINE_PATH` env var) — used as-is;
/// 2. `<resource_dir>/resources/lw-engine[.exe]` (bundled install);
/// 3. `<dev_dir>/lw-engine[.exe]` (development: `go build` output in `engine/`).
pub fn locate_engine_binary(
    override_path: Option<&Path>,
    resource_dir: Option<&Path>,
    dev_dir: Option<&Path>,
) -> Option<PathBuf> {
    if let Some(p) = override_path {
        return p.is_file().then(|| p.to_path_buf());
    }
    let name = binary_file_name();
    let candidates = [
        resource_dir.map(|d| d.join("resources").join(&name)),
        dev_dir.map(|d| d.join(&name)),
    ];
    candidates.into_iter().flatten().find(|p| p.is_file())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmpdir(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("lw-locate-{}-{}", name, std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn priority_order() {
        let res = tmpdir("res");
        let dev = tmpdir("dev");
        assert_eq!(locate_engine_binary(None, Some(&res), Some(&dev)), None);

        let dev_bin = dev.join(binary_file_name());
        std::fs::write(&dev_bin, b"x").unwrap();
        assert_eq!(
            locate_engine_binary(None, Some(&res), Some(&dev)),
            Some(dev_bin.clone())
        );

        std::fs::create_dir_all(res.join("resources")).unwrap();
        let res_bin = res.join("resources").join(binary_file_name());
        std::fs::write(&res_bin, b"x").unwrap();
        assert_eq!(
            locate_engine_binary(None, Some(&res), Some(&dev)),
            Some(res_bin)
        );

        // An override wins, but only if it exists.
        assert_eq!(
            locate_engine_binary(Some(&dev_bin), Some(&res), Some(&dev)),
            Some(dev_bin)
        );
        assert_eq!(
            locate_engine_binary(Some(&res.join("missing")), Some(&res), Some(&dev)),
            None
        );
    }
}
