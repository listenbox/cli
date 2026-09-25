//! Manual visual inspection of the production components with explicitly synthetic fixtures.
#![allow(dead_code)]
#[path = "../src/platform.rs"]
mod platform;
#[path = "../src/quit.rs"]
mod quit;
#[path = "../src/tokens.rs"]
mod tokens;
mod workspace {
    include!("../src/workspace.rs");

    pub fn populate(view: &mut Workspace, window: &mut Window, cx: &mut Context<Workspace>) {
        view.catalog.teams = vec![listenbox_sync_engine::publicapi::ClientTeam {
            id: "team_0123456789abcdef".into(),
            name: "Field Notes Studio".into(),
        }];
        view.catalog.shows = vec![serde_json::from_value(serde_json::json!({
            "id": "shw_0123456789abcdef", "team_id": "team_0123456789abcdef",
            "title": "Field Notes", "slug": "field-notes", "language": "en", "source_kind": "audio",
            "has_active_subscription": true, "youtube_destination": false,
            "youtube_source_url": "https://www.youtube.com/playlist?list=PLpreview"
        })).unwrap()];
        view.loaded = true;
        view.select(Some("field-notes".into()), window, cx);
        let manager = view.client.downloads();
        let work = manager.enqueue(
            "field-notes",
            "Field Notes",
            &[
                (
                    "preview-a".into(),
                    "A conversation about making things".into(),
                ),
                ("preview-b".into(), "The longer way home".into()),
                ("preview-c".into(), "Recording outside the studio".into()),
            ],
        );
        work[0].phase(Phase::Downloading);
        work[0].start_download(24_000_000, 1_000_000);
        for start in (0..12_000_000).step_by(1_000_000) {
            work[0].range(
                start,
                1_000_000,
                listenbox_sync_engine::downloads::RangePhase::Complete,
            );
        }
        work[1].phase(Phase::Preparing);
        work[2].phase(Phase::Queued);
        view.progress = manager.snapshot();
    }
}

#[cfg(target_os = "macos")]
fn main() -> anyhow::Result<()> {
    use gpui_kit::component::{Root, Theme, ThemeMode};
    use gpui_kit::test::TestWindowExt;
    use gpui_kit::{AppContext, HeadlessAppContext, px, size};
    use listenbox_sync_engine::{client::Client, config::Config};
    use std::sync::Arc;
    use tokio_util::{sync::CancellationToken, task::TaskTracker};
    let profile = tempfile::tempdir()?;
    let runtime = Arc::new(tokio::runtime::Runtime::new()?);
    let text = gpui_kit::platform::current_platform(true).text_system();
    let mut cx = HeadlessAppContext::with_platform(
        text,
        Arc::new(gpui_kit::assets::AllAssets),
        gpui_kit::platform::current_headless_renderer,
    );
    cx.update(gpui_kit::init);
    std::fs::create_dir_all("dist/preview")?;
    for (name, mode, width, populated) in [
        ("welcome", ThemeMode::Light, 1080., false),
        ("workspace-light", ThemeMode::Light, 1080., true),
        ("workspace-dark", ThemeMode::Dark, 840., true),
    ] {
        let client = Client::desktop(Config::load_in(None, profile.path().into())?)?;
        let handle = cx.open_window(size(px(width), px(760.)), |window, cx| {
            Theme::change(mode, Some(window), cx);
            tokens::project(cx);
            let view = cx.new(|cx| {
                let mut view = workspace::Workspace::new(
                    client,
                    runtime.clone(),
                    CancellationToken::new(),
                    TaskTracker::new(),
                    window,
                    cx,
                );
                if populated {
                    workspace::populate(&mut view, window, cx);
                }
                view
            });
            cx.new(|cx| Root::new(view, window, cx))
        })?;
        cx.run_until_parked();
        cx.update_window(handle.into(), |_, window, cx| window.render_frame(cx))?;
        cx.capture_screenshot(handle.into())?
            .save(format!("dist/preview/{name}.png"))?;
        cx.update_window(handle.into(), |_, window, _| window.remove_window())?;
    }
    Ok(())
}

#[cfg(not(target_os = "macos"))]
fn main() {
    eprintln!("The visual capture target uses macOS Metal.");
}
