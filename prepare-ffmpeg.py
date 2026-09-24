#!/usr/bin/env python3
"""Build the pinned static FFmpeg libraries with Python's standard library."""

import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import urllib.request


cache = Path(__file__).resolve().parent / ".cache"
prefix = cache / "ffmpeg"
version = "9.0.2"
checksum = "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e"
archive = cache / f"ffmpeg-{version}.tar.xz"
source = cache / f"ffmpeg-{version}"
url = f"https://ffmpeg.org/releases/ffmpeg-{version}.tar.xz"

cache.mkdir(parents=True, exist_ok=True)
with urllib.request.urlopen(url, timeout=30) as response:
    data = response.read()
if hashlib.sha256(data).hexdigest() != checksum:
    raise ValueError("FFmpeg source checksum mismatch")
archive.write_bytes(data)
with tarfile.open(archive) as compressed:
    compressed.extractall(cache, filter="data")
subprocess.run([
    "./configure", f"--prefix={prefix}",
    "--disable-everything", "--disable-autodetect", "--disable-programs",
    "--disable-doc", "--disable-debug", "--disable-network", "--disable-shared",
    "--enable-static", "--enable-pic", "--disable-avdevice", "--disable-avfilter",
    "--disable-swscale", "--enable-swresample", "--enable-protocol=file",
    "--enable-demuxer=mov,matroska,ogg,aac", "--enable-muxer=ipod,mp4,hls,mpegts",
    "--enable-decoder=aac,opus,vorbis,h264", "--enable-encoder=aac",
    "--enable-parser=aac,opus,vorbis,h264", "--enable-bsf=aac_adtstoasc,h264_mp4toannexb",
    "--disable-x86asm",
], cwd=source, check=True)
subprocess.run(["make", f"-j{os.cpu_count() or 1}"], cwd=source, check=True)
subprocess.run(["make", "install"], cwd=source, check=True)
shutil.copyfile(source / "COPYING.LGPLv2.1", prefix / "COPYING.LGPLv2.1")
(prefix / "NOTICE.txt").write_text(
    f"FFmpeg {version}\nSource: {url}\nSHA-256: {checksum}\n"
    "Configuration: see prepare-ffmpeg.py\n"
    + (source / "COPYING.LGPLv2.1").read_text()
)
