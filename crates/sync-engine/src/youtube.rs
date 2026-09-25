use crate::{
    api::{Api, string},
    download::download,
    events::Progress,
    innertube::YouTube,
    publicapi as p,
};
use anyhow::{Context, Result, ensure};
use serde_json::json;
use sha2::{Digest, Sha256};
use std::{collections::HashSet, path::Path, time::Duration};
use url::Url;

pub fn is_source(source: &str) -> bool {
    Url::parse(source).ok().is_some_and(|url| {
        matches!(
            url.host_str(),
            Some("youtube.com" | "www.youtube.com" | "m.youtube.com" | "youtu.be")
        )
    })
}

pub async fn import(api: &Api, source: &str, requested_slug: Option<&str>) -> Result<()> {
    let url = Url::parse(source)?;
    ensure!(
        url.scheme() == "https"
            && url.username().is_empty()
            && url.password().is_none()
            && url.port().is_none(),
        "invalid YouTube source URL"
    );
    let youtube = YouTube::new(api).await?;
    let (title, videos) =
        if let Some((_, playlist)) = url.query_pairs().find(|(key, _)| key == "list") {
            ensure!(valid_youtube_id(&playlist), "invalid playlist ID");
            youtube.playlist(api, &playlist).await?
        } else {
            let id = if url.host_str() == Some("youtu.be") {
                url.path().trim_matches('/').to_owned()
            } else if let Some((_, id)) = url.query_pairs().find(|(key, _)| key == "v") {
                id.into_owned()
            } else {
                url.path()
                    .strip_prefix("/shorts/")
                    .or_else(|| url.path().strip_prefix("/embed/"))
                    .unwrap_or("")
                    .into()
            };
            ensure!(
                id.len() == 11 && valid_youtube_id(&id),
                "invalid YouTube video ID"
            );
            let media = youtube.media(api, &id).await?;
            (media.title, vec![id])
        };
    ensure!(!videos.is_empty(), "YouTube playlist has no public videos");
    let slug = requested_slug
        .map(str::to_owned)
        .unwrap_or_else(|| format!("youtube-{}", &hex::encode(Sha256::digest(source))[..16]));
    let slug = crate::slug(&slug).map_err(anyhow::Error::msg)?;
    let shows: Vec<p::Show> =
        serde_json::from_value(api.json(api.client().list_shows(), &[200]).await?)?;
    let show = match shows.into_iter().find(|show| show.slug == slug) {
        Some(show) => {
            ensure!(
                show.source_kind == p::ShowSourceKind::Video,
                "show {slug:?} is not a video show"
            );
            show
        }
        None => {
            let response = api
                .json(
                    api.client().create_show(p::CreateShowParams {
                        body: p::CreateShow {
                            id: format!("shw_{}", &uuid::Uuid::new_v4().simple().to_string()[..16]),
                            title,
                            slug: slug.clone(),
                            source_kind: p::ShowSourceKind::Video,
                            language: "en".into(),
                            image_asset_id: None,
                        },
                    }),
                    &[201],
                )
                .await?;
            serde_json::from_value(response)?
        }
    };
    let mut progress = Progress::default();
    let mut seen = HashSet::new();
    for (index, id) in videos.iter().enumerate() {
        ensure!(
            id.len() == 11 && valid_youtube_id(id),
            "invalid YouTube playlist video ID"
        );
        if seen.insert(id) {
            import_video(
                api,
                &youtube,
                VideoImport {
                    slug: &slug,
                    id,
                    collection: None,
                    transfer: None,
                    journal: None,
                    audio: false,
                },
            )
            .await
            .with_context(|| format!("import YouTube video {id:?}"))?;
        }
        progress.update(((index + 1) * 100 / videos.len()) as i64)?;
    }
    progress.update(100)?;
    println!("{slug}");
    eprintln!(
        "Open in Listenbox: {}",
        api.config.show_url(&show.team_id, &show.id)?
    );
    Ok(())
}

fn valid_youtube_id(id: &str) -> bool {
    !id.is_empty()
        && id
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'_' || c == b'-')
}

pub(crate) struct VideoImport<'a> {
    pub slug: &'a str,
    pub id: &'a str,
    pub collection: Option<&'a str>,
    pub transfer: Option<&'a crate::downloads::Transfer>,
    pub journal: Option<&'a crate::database::Database>,
    pub audio: bool,
}

pub(crate) async fn import_video(
    api: &Api,
    youtube: &YouTube,
    work: VideoImport<'_>,
) -> Result<()> {
    let VideoImport {
        slug,
        id,
        collection,
        transfer,
        journal,
        audio,
    } = work;
    let source_url = format!("https://www.youtube.com/watch?v={id}");
    let operation_id = match journal {
        Some(journal) => journal.operation(
            &api.config.api_origin,
            slug,
            &source_url,
            collection.unwrap_or(""),
        )?,
        None => uuid::Uuid::new_v4().to_string(),
    };
    let temporary = if journal.is_none() {
        Some(tempfile::tempdir()?)
    } else {
        None
    };
    let directory = match journal {
        Some(journal) => journal.directory(&operation_id)?,
        None => temporary
            .as_ref()
            .context("Missing transfer directory")?
            .path()
            .to_owned(),
    };
    let saved = journal
        .map(|journal| journal.prepared(&operation_id))
        .transpose()?
        .flatten();
    let manifest = match saved {
        Some(manifest) => {
            for object in &manifest.objects {
                let (length, hash) =
                    crate::episodes::file_hash(&directory.join(&object.name)).await?;
                ensure!(
                    length as i64 == object.byte_length && hash == object.sha256,
                    "Prepared media changed on disk; cannot resume this upload"
                );
            }
            manifest
        }
        None => {
            for name in ["audio.m4a", "video.mp4", "hls"] {
                let path = directory.join(name);
                if path.is_dir() {
                    std::fs::remove_dir_all(path)?;
                } else if path.exists() {
                    std::fs::remove_file(path)?;
                }
            }
            let media = youtube.media(api, id).await?;
            if let Some(transfer) = transfer {
                transfer.title(&media.title);
            }
            if audio {
                download(
                    api,
                    media.audio.as_ref().unwrap_or(&media.video),
                    &directory.join("source-audio"),
                    transfer,
                    journal.map(|journal| (journal, operation_id.as_str())),
                )
                .await?;
            } else {
                download(
                    api,
                    &media.video,
                    &directory.join("source-video"),
                    transfer,
                    journal.map(|journal| (journal, operation_id.as_str())),
                )
                .await?;
                if let Some(stream) = &media.audio {
                    download(
                        api,
                        stream,
                        &directory.join("source-audio"),
                        transfer,
                        journal.map(|journal| (journal, operation_id.as_str())),
                    )
                    .await?;
                }
            }
            if let Some(transfer) = transfer {
                transfer.phase(crate::downloads::Phase::Preparing);
            }
            let root = directory.clone();
            let separate_audio = media.audio.is_some();
            let cancel = api.cancel.clone();
            // Join the FFmpeg owner before releasing files, including after cancellation.
            let duration = tokio::task::spawn_blocking(move || {
                if audio {
                    let output = root.join("audio.m4a");
                    if output.exists() {
                        std::fs::remove_file(&output)?;
                    }
                    crate::audio::prepare_m4a(&root.join("source-audio"), &output, &cancel)?;
                    crate::audio::duration(&output)
                } else {
                    crate::media::prepare(&root, separate_audio, &cancel)
                }
            })
            .await??;
            let manifest = p::CreateEpisodePackage {
                show_slug: slug.into(),
                source_url: source_url.clone(),
                operation_id,
                source_collection_url: collection.map(str::to_owned),
                title: media.title,
                description: Some(media.description),
                duration_seconds: duration,
                published_at: media.published_at,
                objects: inventory(&directory, audio).await?,
            };
            if let Some(journal) = journal {
                for object in &manifest.objects {
                    std::fs::File::open(directory.join(&object.name))?.sync_all()?;
                }
                journal.save_prepared(&manifest)?;
            }
            manifest
        }
    };
    let objects = &manifest.objects;
    if let Some(transfer) = transfer {
        transfer.phase(crate::downloads::Phase::Uploading);
    }
    let response = api
        .send(
            api.client()
                .create_episode_package(p::CreateEpisodePackageParams {
                    body: manifest.clone(),
                }),
        )
        .await?;
    if response.status() != reqwest::StatusCode::CREATED {
        return Err(api.response_error(response).await);
    }
    // Keep the trace until after reading admission, so every admitted session has a cleanup owner.
    let trace = response
        .headers()
        .get("X-Trace-Id")
        .and_then(|h| h.to_str().ok())
        .map(str::to_owned);
    let session: p::EpisodePackage = api.decode(response).await?;
    if session.status == p::EpisodePackageStatus::Completed {
        if let Some(journal) = journal {
            journal.forget(&api.config.api_origin, slug, &source_url)?;
        }
        return Ok(());
    }
    if let Some(journal) = journal {
        journal.session(&manifest.operation_id, &session.upload_session_id)?;
    }
    let result: Result<()> = async {
        if api.config.print_trace_ids {
            let trace = trace.context("response missing X-Trace-Id")?;
            crate::config::check_trace(&trace)?;
            println!("Trace ID: {trace}");
        }
        ensure!(session.part_size >= 5 << 20, "invalid package part size");
        for (ordinal, object) in objects.iter().enumerate() {
            let mut offset = 0;
            let mut number = 1;
            while offset < object.byte_length {
                let length = session.part_size.min(object.byte_length - offset);
                if journal
                    .map(|journal| journal.has_part(&manifest.operation_id, ordinal, number))
                    .transpose()?
                    .unwrap_or(false)
                {
                    offset += length;
                    number += 1;
                    continue;
                }
                let signed = api
                    .json(
                        api.client().presign_episode_package_parts(
                            p::PresignEpisodePackagePartsParams {
                                upload_session_id: session.upload_session_id.clone(),
                                object_index: ordinal as i64,
                                body: p::PresignEpisodeUploadSessionParts {
                                    part_numbers: vec![number],
                                },
                            },
                        ),
                        &[200],
                    )
                    .await?;
                let parts = signed["parts"]
                    .as_array()
                    .context("missing package signed parts")?;
                if parts.is_empty() {
                    break;
                }
                ensure!(
                    parts.len() == 1 && parts[0]["part_number"] == number,
                    "invalid signed package part"
                );
                let length = session.part_size.min(object.byte_length - offset);
                crate::episodes::upload_part(
                    api,
                    &directory.join(&object.name),
                    offset as u64,
                    length as u64,
                    "PUT",
                    string(&parts[0], "upload_url")?,
                )
                .await?;
                if let Some(journal) = journal {
                    journal.save_part(&manifest.operation_id, ordinal, number)?;
                }
                offset += length;
                number += 1;
            }
        }
        let completed = api
            .json(
                api.client()
                    .complete_episode_package(p::CompleteEpisodePackageParams {
                        upload_session_id: session.upload_session_id.clone(),
                    }),
                &[200],
            )
            .await?;
        ensure!(
            string(&completed, "status")? == "completed",
            "prepared media did not complete"
        );
        Ok(())
    }
    .await;
    if let Err(error) = result {
        if journal.is_some() {
            return Err(error);
        }
        let mut cleanup = api.clone();
        cleanup.cancel = tokio_util::sync::CancellationToken::new();
        let request = cleanup
            .client()
            .cancel_episode_package(p::CancelEpisodePackageParams {
                upload_session_id: session.upload_session_id,
            });
        let cancelled = tokio::time::timeout(Duration::from_secs(10), cleanup.send(request)).await;
        match cancelled {
            Ok(Ok(response)) if [204, 409].contains(&response.status().as_u16()) => {}
            Ok(Ok(response)) => {
                return Err(error).context(format!(
                    "cancel pending package returned HTTP {}",
                    response.status()
                ));
            }
            Ok(Err(cleanup_error)) => {
                return Err(error).context(format!("cancel pending package: {cleanup_error}"));
            }
            Err(_) => return Err(error).context("cancel pending package timed out"),
        }
        return Err(error);
    }
    if let Some(journal) = journal {
        journal.forget(&api.config.api_origin, slug, &source_url)?;
    }
    Ok(())
}

async fn inventory(root: &Path, audio: bool) -> Result<Vec<p::PreparedMediaObject>> {
    let mut directories = vec![root.to_owned()];
    let mut names = Vec::new();
    while let Some(directory) = directories.pop() {
        for entry in std::fs::read_dir(directory)? {
            let entry = entry?;
            if entry.file_type()?.is_dir() {
                directories.push(entry.path());
                continue;
            }
            ensure!(
                entry.file_type()?.is_file(),
                "media package contains a non-regular object"
            );
            let path = entry.path();
            let name = path
                .strip_prefix(root)?
                .to_string_lossy()
                .replace('\\', "/");
            if (audio && name == "audio.m4a")
                || (!audio && (name == "video.mp4" || name.starts_with("hls/")))
            {
                names.push(name);
            }
        }
    }
    names.sort();
    let mut objects = Vec::new();
    for name in names {
        let content_type = if name.ends_with(".m3u8") {
            "application/vnd.apple.mpegurl"
        } else if name.starts_with("hls/audio/") || name == "audio.m4a" {
            "audio/mp4"
        } else {
            "video/mp4"
        };
        let (length, sha256) = crate::episodes::file_hash(&root.join(&name)).await?;
        objects.push(p::PreparedMediaObject {
            name,
            content_type: serde_json::from_value(json!(content_type))?,
            byte_length: length as i64,
            sha256,
        });
    }
    Ok(objects)
}
