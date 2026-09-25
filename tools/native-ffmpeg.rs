//! Pinned native dependency preparation, shared by every client target.
use std::{env, error::Error, fs, path::Path, process::Command};

fn run(command: &mut Command) -> Result<(), Box<dyn Error>> {
    let status = command.status()?;
    if !status.success() {
        return Err(format!("{command:?} failed: {status}").into());
    }
    Ok(())
}

fn main() -> Result<(), Box<dyn Error>> {
    let version = "9.0.2";
    let checksum = "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e";
    let cache = env::current_dir()?.join(".cache");
    let prefix = cache.join("ffmpeg");
    fs::create_dir_all(&cache)?;
    let archive = cache.join(format!("ffmpeg-{version}.tar.xz"));
    let url = format!("https://ffmpeg.org/releases/ffmpeg-{version}.tar.xz");
    if !archive.exists() {
        run(Command::new("curl")
            .args(["--fail", "--location", "--max-time", "60", "--output"])
            .arg(&archive)
            .arg(&url))?;
    }
    let digest = Command::new("openssl")
        .args(["dgst", "-sha256"])
        .arg(&archive)
        .output()?;
    if !digest.status.success() || !String::from_utf8(digest.stdout)?.trim().ends_with(checksum) {
        return Err("FFmpeg source checksum mismatch".into());
    }
    run(Command::new("tar")
        .arg("-xf")
        .arg(&archive)
        .arg("-C")
        .arg(&cache))?;
    let source = cache.join(format!("ffmpeg-{version}"));
    run(Command::new(source.join("configure"))
        .current_dir(&source)
        .arg(format!("--prefix={}", prefix.display()))
        .args([
            "--disable-everything",
            "--disable-autodetect",
            "--disable-programs",
            "--disable-doc",
            "--disable-debug",
            "--disable-network",
            "--disable-shared",
            "--enable-static",
            "--enable-pic",
            "--disable-avdevice",
            "--disable-avfilter",
            "--disable-swscale",
            "--enable-swresample",
            "--enable-protocol=file",
            "--enable-demuxer=mov,matroska,ogg,aac",
            "--enable-muxer=ipod,mp4,hls,mpegts",
            "--enable-decoder=aac,opus,vorbis,h264",
            "--enable-encoder=aac",
            "--enable-parser=aac,opus,vorbis,h264",
            "--enable-bsf=aac_adtstoasc,h264_mp4toannexb",
            "--disable-x86asm",
        ]))?;
    run(Command::new("make")
        .current_dir(&source)
        .arg(format!("-j{}", std::thread::available_parallelism()?)))?;
    run(Command::new("make").current_dir(&source).arg("install"))?;
    fs::copy(
        source.join("COPYING.LGPLv2.1"),
        prefix.join("COPYING.LGPLv2.1"),
    )?;
    fs::write(
        prefix.join("NOTICE.txt"),
        format!(
            "FFmpeg {version}\nSource: {url}\nSHA-256: {checksum}\nConfiguration: tools/native-ffmpeg.rs\n{}",
            fs::read_to_string(source.join(Path::new("COPYING.LGPLv2.1")))?
        ),
    )?;
    Ok(())
}
