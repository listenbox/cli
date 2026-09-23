import { execFile } from "node:child_process"
import { chmod, copyFile, mkdir, writeFile } from "node:fs/promises"
import { delimiter, join } from "node:path"
import { promisify } from "node:util"

const run = promisify(execFile)
const destination = new URL("./dist/", import.meta.url)
await mkdir(destination, { recursive: true })

async function bundle(name: string) {
  const executable = process.platform === "win32" ? `${name}.exe` : name
  const { stdout } = await run(executable, ["-version"])
  const version = new RegExp(`^${name} version (?:n)?(\\d+)\\.`).exec(stdout)
  if (version === null || Number(version[1]) < 9) {
    throw new Error(`The CLI bundle requires ${name} 9 or newer: ${stdout.split("\n")[0]}`)
  }
  for (const directory of (process.env.PATH ?? "").split(delimiter)) {
    try {
      await copyFile(join(directory, executable), new URL(executable, destination))
      await chmod(new URL(executable, destination), 0o755)
      const license = await run(executable, ["-L"])
      await writeFile(new URL(`${name}-NOTICE.txt`, destination), `${stdout}\n${license.stdout}\n${license.stderr}\nUpstream source: https://github.com/FFmpeg/FFmpeg\nThe version and build configuration above identify this bundled build.\n`)
      return
    } catch (error) {
      if (!(error instanceof Error) || !("code" in error) || error.code !== "ENOENT") throw error
    }
  }
  throw new Error(`Cannot locate ${executable} for the CLI bundle`)
}

await bundle("ffmpeg")
await bundle("ffprobe")
