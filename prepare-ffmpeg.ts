#!/usr/bin/env nub

import { createHash } from "node:crypto"
import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises"
import { availableParallelism } from "node:os"
import { join } from "node:path"

import { $ } from "zx"

const cache = join(import.meta.dirname, ".cache")
const prefix = join(cache, "ffmpeg")
const version = "9.0.2"
const checksum =
  "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e"
const archive = join(cache, `ffmpeg-${version}.tar.xz`)
const source = join(cache, `ffmpeg-${version}`)
const url = `https://ffmpeg.org/releases/ffmpeg-${version}.tar.xz`

const response = await fetch(url, { signal: AbortSignal.timeout(30_000) })
if (!response.ok)
  throw new Error(`FFmpeg download failed: HTTP ${response.status}`)
const data = Buffer.from(await response.arrayBuffer())
if (createHash("sha256").update(data).digest("hex") !== checksum) {
  throw new Error("FFmpeg source checksum mismatch")
}
await mkdir(cache, { recursive: true })
await writeFile(archive, data)
await $`tar -xf ${archive} -C ${cache}`

const run = $({ cwd: source, stdio: "inherit" })
await run`./configure ${[
  `--prefix=${prefix}`,
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
]}`
await run`make -j${availableParallelism()}`
await run`make install`
await copyFile(
  join(source, "COPYING.LGPLv2.1"),
  join(prefix, "COPYING.LGPLv2.1"),
)
await writeFile(
  join(prefix, "NOTICE.txt"),
  `FFmpeg ${version}\nSource: ${url}\nSHA-256: ${checksum}\n` +
    "Configuration: see prepare-ffmpeg.ts\n" +
    (await readFile(join(source, "COPYING.LGPLv2.1"), "utf8")),
)
