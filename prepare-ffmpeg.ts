import { spawn } from "node:child_process"
import { createHash } from "node:crypto"
import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises"
import { availableParallelism } from "node:os"
import { join, resolve } from "node:path"

const cache = resolve(import.meta.dirname, ".cache")
const prefix = join(cache, "ffmpeg")
const version = "9.0.2"
const checksum =
  "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e"
const archive = join(cache, `ffmpeg-${version}.tar.xz`)
const source = join(cache, `ffmpeg-${version}`)

async function run(command: string, args: string[], cwd: string) {
  await new Promise<void>((resolve, reject) => {
    const child = spawn(command, args, { cwd, stdio: "inherit" })
    child.once("error", reject)
    child.once("exit", (code, signal) => {
      if (code === 0) resolve()
      else reject(new Error(`${command} failed: ${signal ?? code}`))
    })
  })
}

await mkdir(cache, { recursive: true })
const response = await fetch(
  `https://ffmpeg.org/releases/ffmpeg-${version}.tar.xz`,
)
if (!response.ok) throw new Error(`Download FFmpeg: HTTP ${response.status}`)
const bytes = Buffer.from(await response.arrayBuffer())
if (createHash("sha256").update(bytes).digest("hex") !== checksum) {
  throw new Error("FFmpeg source checksum mismatch")
}
await writeFile(archive, bytes)
await run("tar", ["-xf", archive, "-C", cache], cache)
await run(
  "./configure",
  [
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
  ],
  source,
)
await run("make", [`-j${availableParallelism()}`], source)
await run("make", ["install"], source)
await copyFile(
  join(source, "COPYING.LGPLv2.1"),
  join(prefix, "COPYING.LGPLv2.1"),
)
await writeFile(
  join(prefix, "NOTICE.txt"),
  `FFmpeg ${version}\nSource: https://ffmpeg.org/releases/ffmpeg-${version}.tar.xz\nSHA-256: ${checksum}\nConfiguration: see apps/cli/prepare-ffmpeg.ts\n${await readFile(join(source, "COPYING.LGPLv2.1"), "utf8")}`,
)
