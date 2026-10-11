// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

#[path = "../build_support.rs"]
mod build_support;

use std::fs;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

struct TestDir {
    path: PathBuf,
}

impl TestDir {
    fn new(name: &str) -> Self {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("system clock should be after Unix epoch")
            .as_nanos();
        let path = std::env::temp_dir().join(format!(
            "light-whisper-engine-policy-{name}-{}-{nonce}",
            std::process::id()
        ));
        fs::create_dir_all(path.join("resources")).expect("create test resources directory");
        Self { path }
    }

    fn path(&self) -> &Path {
        &self.path
    }
}

impl Drop for TestDir {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.path);
    }
}

#[test]
fn release_build_rejects_a_missing_archive_without_creating_a_placeholder() {
    let test_dir = TestDir::new("release-missing");

    let error = build_support::select_engine_archive(test_dir.path(), false)
        .expect_err("release build must reject a missing archive");

    assert!(error.contains("scripts/build_engine.py"));
    assert!(!test_dir.path().join("resources/engine.tar.xz").exists());
}

#[test]
fn release_build_rejects_an_empty_archive() {
    let test_dir = TestDir::new("release-empty");
    fs::File::create(test_dir.path().join("resources/engine.tar.xz"))
        .expect("create empty archive");

    let error = build_support::select_engine_archive(test_dir.path(), false)
        .expect_err("release build must reject an empty archive");

    assert!(error.contains("非空"));
}

#[test]
fn release_build_accepts_a_non_empty_tar_archive() {
    let test_dir = TestDir::new("release-valid");
    let archive = test_dir.path().join("resources/engine.tar.xz");
    fs::write(&archive, b"archive").expect("write archive fixture");

    assert_eq!(
        build_support::select_engine_archive(test_dir.path(), false).expect("select archive"),
        archive
    );
}

#[test]
fn debug_build_prefers_tar_and_falls_back_to_the_legacy_zip() {
    let test_dir = TestDir::new("debug-priority");
    let tar = test_dir.path().join("resources/engine.tar.xz");
    let zip = test_dir.path().join("resources/engine.zip");
    fs::write(&zip, b"legacy").expect("write legacy archive fixture");

    assert_eq!(
        build_support::select_engine_archive(test_dir.path(), true).expect("select legacy archive"),
        zip
    );

    fs::write(&tar, b"current").expect("write current archive fixture");
    assert_eq!(
        build_support::select_engine_archive(test_dir.path(), true)
            .expect("select current archive"),
        tar
    );
}

#[test]
fn debug_build_creates_a_placeholder_only_when_no_archive_exists() {
    let test_dir = TestDir::new("debug-placeholder");
    let expected = test_dir.path().join("resources/engine.tar.xz");

    let selected =
        build_support::select_engine_archive(test_dir.path(), true).expect("create placeholder");

    assert_eq!(selected, expected);
    assert_eq!(fs::metadata(expected).expect("read placeholder").len(), 0);
}

#[test]
fn directories_are_not_treated_as_archives() {
    let test_dir = TestDir::new("directory");
    fs::create_dir(test_dir.path().join("resources/engine.tar.xz"))
        .expect("create same-name directory");

    assert!(build_support::select_engine_archive(test_dir.path(), false).is_err());
}

#[test]
fn release_go_build_requires_lw_engine_binary() {
    let test_dir = TestDir::new("go-release-missing");
    let error = build_support::select_go_engine_binary(test_dir.path(), false)
        .expect_err("release Go packaging must require lw-engine");
    assert!(error.contains("lw-engine"));
    assert!(error.contains("build_lw_engine_for_bundle"));
}

#[test]
fn release_go_build_accepts_lw_engine_exe() {
    let test_dir = TestDir::new("go-release-exe");
    let exe = test_dir.path().join("resources/lw-engine.exe");
    fs::write(&exe, b"go-engine").expect("write go binary");
    assert_eq!(
        build_support::select_go_engine_binary(test_dir.path(), false).expect("select go exe"),
        exe
    );
}

#[test]
fn default_fingerprint_source_is_go_engine() {
    let test_dir = TestDir::new("fingerprint-go");
    let exe = test_dir.path().join("resources/lw-engine.exe");
    fs::write(&exe, b"go-engine").expect("write go binary");
    // Ensure python archive presence does not override default Go path.
    fs::write(test_dir.path().join("resources/engine.tar.xz"), b"archive").expect("write archive");
    assert_eq!(
        build_support::select_packaging_fingerprint_source(test_dir.path(), false)
            .expect("select go fingerprint"),
        exe
    );
}
