import {
  Constants,
  Innertube,
  Log,
  Platform,
  Player,
  YTNodes,
} from "youtubei.js"
import type { Misc } from "youtubei.js"

declare function hostFetch(request: string): Promise<string>
declare const Buffer: {
  from(
    value: string | ArrayBuffer,
    encoding?: string,
  ): { toString(encoding: string): string }
}

async function fetchThroughRust(input: RequestInfo | URL, init?: RequestInit) {
  const request = new Request(input, init)
  const body =
    request.method === "GET" || request.method === "HEAD"
      ? null
      : Buffer.from(await request.arrayBuffer()).toString("base64")
  const result: {
    error?: string
    body: string
    status: number
    headers: Record<string, string>
  } = JSON.parse(
    await hostFetch(
      JSON.stringify({
        url: request.url,
        method: request.method,
        headers: [...request.headers],
        body,
      }),
    ),
  )
  if (result.error) throw new Error(result.error)
  return new Response(
    Buffer.from(result.body, "base64") as unknown as BodyInit,
    {
      status: result.status,
      headers: result.headers,
    },
  )
}

Platform.load({
  runtime: "unknown",
  server: true,
  fetch: fetchThroughRust,
  Request,
  Response,
  Headers,
  FormData,
  File,
  ReadableStream,
  CustomEvent,
  uuidv4: () => crypto.randomUUID(),
  sha1Hash: async (text: string) =>
    Buffer.from(
      await crypto.subtle.digest("SHA-1", new TextEncoder().encode(text)),
    ).toString("hex"),
  eval: async (data, env) =>
    new Function(...Object.keys(env), data.output)(...Object.values(env)),
} as Parameters<typeof Platform.load>[0])
Log.setLevel(Log.Level.NONE)

let client: Promise<Innertube> | undefined
const cache = new Map<string, ArrayBuffer>()

function session() {
  client ??= Innertube.create({
    lang: "en",
    location: "US",
    retrieve_player: false,
    generate_session_locally: true,
    cache: {
      cache_dir: "",
      get: async key => cache.get(key),
      set: async (key, value) => {
        cache.set(key, value)
      },
      remove: async key => {
        cache.delete(key)
      },
    },
  })
  return client
}

async function stream(yt: Innertube, format: Misc.Format, cpn: string) {
  if (
    format.cipher ||
    format.signature_cipher ||
    (format.url && new URL(format.url).searchParams.has("n"))
  ) {
    yt.session.player ??= await Player.create(
      yt.session.cache,
      fetchThroughRust,
    )
  }
  const url = new URL(await format.decipher(yt.session.player))
  url.searchParams.set("cpn", cpn)
  return {
    url: url.toString(),
    user_agent: Constants.CLIENTS.VISIONOS.USER_AGENT,
  }
}

export async function call(method: string, json: string): Promise<string> {
  try {
    const args: { id: string } = JSON.parse(json)
    const yt = await session()
    if (method === "playlist") {
      let page = await yt.getPlaylist(args.id)
      const title = page.info.title
      const videos: string[] = []
      const seen = new Set<string>()
      const pages = new Set<string>()
      for (;;) {
        const ids: string[] = []
        for (const item of page.items) {
          let id: string
          if (item.is(YTNodes.PlaylistVideo)) {
            if (
              !item.is_playable ||
              item.is_live ||
              item.is_upcoming ||
              item.upcoming
            )
              continue
            id = item.id
          } else if (
            item.is(YTNodes.LockupView) &&
            ["VIDEO", "SHORT"].includes(item.content_type)
          ) {
            if (!item.metadata?.title.toString()) continue
            id = item.content_id
          } else {
            throw new Error("Unsupported playlist item; listing is incomplete")
          }
          ids.push(id)
          if (!seen.has(id)) {
            seen.add(id)
            videos.push(id)
          }
        }
        const fingerprint = ids.join(",")
        if (pages.has(fingerprint) || pages.size >= 10000)
          throw new Error("Repeated YouTube playlist page")
        pages.add(fingerprint)
        if (!page.has_continuation) break
        page = await page.getContinuation()
      }
      if (!videos.length)
        throw new Error("YouTube playlist has no public videos")
      return JSON.stringify({ title, videos })
    }
    if (method !== "media") throw new Error("Unknown YouTube operation")
    const info = await yt.getBasicInfo(args.id, { client: "VISIONOS" })
    if (
      info.playability_status?.status !== "OK" ||
      info.basic_info.is_live ||
      info.basic_info.is_upcoming
    ) {
      throw new Error(
        `YouTube playback unavailable: ${info.playability_status?.reason ?? "not a public recorded video"}`,
      )
    }
    const formats = [
      ...(info.streaming_data?.formats ?? []),
      ...(info.streaming_data?.adaptive_formats ?? []),
    ].filter(
      format =>
        !format.drm_families?.length &&
        !format.is_type_otf &&
        !!(format.url || format.cipher || format.signature_cipher),
    )
    const video = formats
      .filter(
        format =>
          format.has_video &&
          format.mime_type.includes("avc1") &&
          (format.height ?? 0) > 0 &&
          format.height! <= 1080,
      )
      .sort((left, right) => right.height! - left.height!)[0]
    if (!video)
      throw new Error("YouTube video has no AVC rendition at or below 1080p")
    const audio = video.has_audio
      ? undefined
      : formats
          .filter(format => format.has_audio && !format.has_video)
          .sort((left, right) => right.bitrate - left.bitrate)[0]
    if (!video.has_audio && !audio)
      throw new Error("YouTube video has no audio stream")
    return JSON.stringify({
      title: info.basic_info.title ?? args.id,
      description: info.basic_info.short_description ?? "",
      video: await stream(yt, video, info.cpn),
      audio: audio ? await stream(yt, audio, info.cpn) : null,
    })
  } catch (error) {
    return JSON.stringify({
      bridge_error: error instanceof Error ? error.message : String(error),
    })
  }
}
