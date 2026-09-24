//! Application policy over the shared typed youtubei bindings.
use crate::api::{Api, read_bounded};
use anyhow::{Context, Result, bail, ensure};
use std::collections::HashSet;
use youtubei::{
    Client, Engine, EngineOptions, FetchRequest, FetchResponse, Format, GetVideoInfoOptions,
    Innertube, Player, SessionOptions, UniversalCache,
    models::{LockupContentType, PlaylistItem},
};

pub struct YouTube {
    client: Innertube,
}

pub struct Media {
    pub title: String,
    pub description: String,
    pub video: Stream,
    pub audio: Option<Stream>,
}

pub struct Stream {
    pub url: String,
    pub user_agent: String,
}

impl YouTube {
    pub async fn new(api: &Api) -> Result<Self> {
        let engine = Engine::with_options(EngineOptions {
            memory_limit: 256 * 1024 * 1024,
            stack_size: 4 * 1024 * 1024,
        })
        .await?;
        let cancel = api.cancel.clone();
        engine
            .set_interrupt_handler(move || cancel.is_cancelled())
            .await;
        let api = api.clone();
        let fetch = engine
            .fetch_with(move |request| {
                let api = api.clone();
                async move {
                    fetch(&api, request)
                        .await
                        .map_err(|error| youtubei::Error::new(error.to_string()))
                }
            })
            .await?;
        let cache = UniversalCache::new(&engine, false, None).await?;
        let client = Innertube::create_in(
            &engine,
            SessionOptions {
                lang: Some("en".into()),
                location: Some("US".into()),
                cache: Some(cache.as_cache()),
                fetch: Some(fetch),
                ..SessionOptions::local()
            },
        )
        .await
        .context("initialize YouTube")?;
        Ok(Self { client })
    }

    pub async fn playlist(&self, api: &Api, id: &str) -> Result<(String, Vec<String>)> {
        api.wait(async {
            let mut page = self.client.get_playlist(id).await?;
            let mut title = None;
            let mut videos = Vec::new();
            let mut seen = HashSet::new();
            let mut pages = HashSet::new();
            loop {
                let data = page.data().await?;
                if title.is_none() {
                    title = data.info.title;
                }
                let mut ids = Vec::new();
                for item in data.items {
                    let id = match item {
                        PlaylistItem::PlaylistVideo(video) => {
                            if !video.is_playable
                                || video.is_live
                                || video.is_upcoming
                                || video.upcoming.is_some()
                            {
                                continue;
                            }
                            video.id
                        }
                        PlaylistItem::LockupView(video)
                            if matches!(
                                video.content_type,
                                LockupContentType::Video | LockupContentType::Short
                            ) =>
                        {
                            if !video
                                .metadata
                                .and_then(|m| m.title)
                                .is_some_and(|title| !title.as_str().is_empty())
                            {
                                continue;
                            }
                            video.content_id
                        }
                        _ => bail!("Unsupported playlist item; listing is incomplete"),
                    };
                    ids.push(id.clone());
                    if seen.insert(id.clone()) {
                        videos.push(id);
                    }
                }
                ensure!(
                    pages.len() < 10000 && pages.insert(ids),
                    "Repeated YouTube playlist page"
                );
                if !data.has_continuation {
                    break;
                }
                page = page.get_continuation().await?;
            }
            ensure!(!videos.is_empty(), "YouTube playlist has no public videos");
            Ok((
                title
                    .filter(|title| !title.is_empty())
                    .context("YouTube playlist missing title")?,
                videos,
            ))
        })
        .await
    }

    pub async fn media(&self, api: &Api, id: &str) -> Result<Media> {
        api.wait(async {
            let info = self
                .client
                .get_basic_info(
                    id,
                    GetVideoInfoOptions {
                        client: Some(Client::VisionOs),
                        ..Default::default()
                    },
                )
                .await?;
            let data = info.data().await?;
            ensure!(
                data.playability_status
                    .as_ref()
                    .is_some_and(|s| s.status == "OK")
                    && !data.basic_info.is_live.unwrap_or(false)
                    && !data.basic_info.is_upcoming.unwrap_or(false),
                "YouTube playback unavailable: {}",
                data.playability_status
                    .and_then(|s| s.reason)
                    .unwrap_or_else(|| "not a public recorded video".into())
            );
            let mut formats = info.formats().await?;
            formats.extend(info.adaptive_formats().await?);
            formats.retain(|format| {
                let f = format.info();
                f.drm_families.as_ref().is_none_or(Vec::is_empty)
                    && !f.is_type_otf
                    && (f.url.is_some() || f.cipher.is_some() || f.signature_cipher.is_some())
            });
            let video = formats
                .iter()
                .filter(|format| {
                    let f = format.info();
                    f.has_video
                        && f.mime_type.contains("avc1")
                        && f.height
                            .is_some_and(|height| height > 0.0 && height <= 1080.0)
                })
                .max_by(|a, b| {
                    a.info()
                        .height
                        .unwrap()
                        .total_cmp(&b.info().height.unwrap())
                })
                .context("YouTube video has no AVC rendition at or below 1080p")?;
            let audio = if video.info().has_audio {
                None
            } else {
                Some(
                    formats
                        .iter()
                        .filter(|format| format.info().has_audio && !format.info().has_video)
                        .max_by(|a, b| a.info().bitrate.total_cmp(&b.info().bitrate))
                        .context("YouTube video has no audio stream")?,
                )
            };
            Ok(Media {
                title: data.basic_info.title.unwrap_or_else(|| id.into()),
                description: data.basic_info.short_description.unwrap_or_default(),
                video: self.stream(video, &data.cpn).await?,
                audio: match audio {
                    Some(format) => Some(self.stream(format, &data.cpn).await?),
                    None => None,
                },
            })
        })
        .await
    }

    async fn stream(&self, format: &Format, cpn: &str) -> Result<Stream> {
        let session = self.client.session().await?;
        let info = format.info();
        let needs_player = info.cipher.is_some()
            || info.signature_cipher.is_some()
            || info.url.as_ref().is_some_and(|url| {
                url::Url::parse(url).is_ok_and(|url| url.query_pairs().any(|(key, _)| key == "n"))
            });
        if needs_player && session.player().await?.is_none() {
            let player = Player::create(
                self.client.engine(),
                session.cache().await?.as_ref(),
                Some(&session.fetch().await?),
                None,
                None,
            )
            .await?;
            session.set_player(&player).await?;
        }
        let mut url = url::Url::parse(&format.decipher(session.player().await?.as_ref()).await?)?;
        url.query_pairs_mut().append_pair("cpn", cpn);
        Ok(Stream {
            url: url.into(),
            user_agent: Client::VisionOs
                .user_agent(self.client.engine())
                .await?
                .context("YouTube VISIONOS user agent missing")?,
        })
    }
}

async fn fetch(api: &Api, input: FetchRequest) -> Result<FetchResponse> {
    let url = url::Url::parse(&input.url)?;
    let host = url.host_str().unwrap_or("");
    ensure!(
        url.scheme() == "https"
            && [
                "youtube.com",
                "google.com",
                "googleapis.com",
                "googlevideo.com",
                "ytimg.com"
            ]
            .iter()
            .any(|base| host == *base || host.ends_with(&format!(".{base}"))),
        "unexpected YouTube API host"
    );
    let mut request = api.http.request(input.method.parse()?, url);
    for (name, value) in input.headers {
        if !["host", "content-length", "cookie"].contains(&name.to_ascii_lowercase().as_str()) {
            request = request.header(name, value);
        }
    }
    if let Some(body) = input.body {
        request = request.body(body);
    }
    let response = api.send(request).await?;
    let status = response.status().as_u16();
    let headers = response
        .headers()
        .iter()
        .filter_map(|(key, value)| {
            value
                .to_str()
                .ok()
                .map(|value| (key.to_string(), value.to_owned()))
        })
        .collect();
    let body = api.wait(read_bounded(response, 32 << 20)).await?;
    Ok(FetchResponse {
        status,
        headers,
        body,
    })
}
