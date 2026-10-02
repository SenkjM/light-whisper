use tauri::image::Image;

pub(crate) fn size_for_scale(scale: f64) -> u32 {
    if scale >= 2.0 {
        32
    } else if scale >= 1.5 {
        24
    } else if scale >= 1.25 {
        20
    } else {
        16
    }
}

pub(crate) fn image(size: u32, recording: bool) -> Image<'static> {
    let rgba: &[u8] = match (size, recording) {
        (16, false) => include_bytes!("../icons/tray-16.rgba"),
        (16, true) => include_bytes!("../icons/tray-16-recording.rgba"),
        (20, false) => include_bytes!("../icons/tray-20.rgba"),
        (20, true) => include_bytes!("../icons/tray-20-recording.rgba"),
        (24, false) => include_bytes!("../icons/tray-24.rgba"),
        (24, true) => include_bytes!("../icons/tray-24-recording.rgba"),
        (32, false) => include_bytes!("../icons/tray-32.rgba"),
        (32, true) => include_bytes!("../icons/tray-32-recording.rgba"),
        _ => return image(16, recording),
    };
    Image::new_owned(rgba.to_vec(), size, size)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tray_marks_cover_windows_scales_and_recording_state() {
        for (scale, size) in [(1.0, 16), (1.25, 20), (1.5, 24), (2.0, 32)] {
            assert_eq!(size_for_scale(scale), size);
            let idle = image(size, false);
            let active = image(size, true);
            assert_eq!(idle.rgba().len(), (size * size * 4) as usize);
            assert_eq!((idle.width(), idle.height()), (size, size));
            assert_ne!(idle.rgba(), active.rgba());
            assert!(idle.rgba().chunks_exact(4).any(|pixel| pixel[3] == 0));
            assert!(idle.rgba().chunks_exact(4).any(|pixel| pixel[3] == 255));
        }
    }
}
