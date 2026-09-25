use super::*;
// The GPUI glob also re-exports its #[test] macro. Generated Rust test
// attributes must resolve to the built-in macro, not recursively to GPUI.
use core::prelude::v1::test;
use gpui_kit::component::Root;
use gpui_kit::test::TestWindowExt;
use listenbox_sync_engine::config::Config;

// Runs against the real ephemeral Listenbox API from apps/api/e2e. Standalone
// checks do not silently substitute a fake API; the parent explicitly invokes it.
#[gpui_kit::test]
#[ignore = "requires the parent workspace's ephemeral Listenbox services"]
async fn live_backend(cx: &mut TestAppContext) {
    let runtime = Arc::new(tokio::runtime::Runtime::new().unwrap());
    let client = Client::desktop(Config::load(None).unwrap()).unwrap();
    let cancel = CancellationToken::new();
    cx.update(gpui_kit::init);
    cx.executor().allow_parking();
    let mut workspace = None;
    let handle = cx.open_window(size(px(1080.), px(760.)), |window, cx| {
        tokens::apply(window, cx);
        let view = cx.new(|cx| {
            Workspace::new(
                client,
                runtime.clone(),
                cancel.clone(),
                TaskTracker::new(),
                window,
                cx,
            )
        });
        workspace = Some(view.clone());
        crate::platform::install_actions(&view, cx);
        Root::new(view, window, cx)
    });
    let workspace = workspace.unwrap();
    wait_for(cx, &workspace, |view| !view.loading).await;
    cx.update_window(handle.into(), |_, window, cx| {
        let view = workspace.read(cx);
        assert!(view.loaded, "catalog failed: {:?}", view.error);
        assert!(!view.catalog.teams.is_empty());
        assert_eq!(view.catalog.shows.len(), 1);
        window.render_frame(cx);
        assert_ne!(window.find("sync-now").disabled(), Some(true));
        window.click("team-picker", cx);
        let team_id = workspace.read(cx).catalog.teams[0].id.clone();
        window.click(SharedString::from(format!("team-{team_id}")), cx);
        assert_eq!(workspace.read(cx).team.as_ref(), Some(&team_id));
        window.click("open-show", cx);
        window.click("sync-now", cx);
        assert_eq!(workspace.read(cx).jobs.len(), 1);
    })
    .unwrap();
    assert!(cx.opened_url().is_some());
    wait_for(cx, &workspace, |view| view.jobs.is_empty()).await;
    cx.update_window(handle.into(), |_, window, cx| {
        let view = workspace.read(cx);
        assert!(
            view.reports
                .values()
                .any(|report| report.starts_with("1 added")),
            "reports: {:?}",
            view.reports
        );
        window.render_frame(cx);
        assert_ne!(window.find("sync-now").disabled(), Some(true));
        assert!(window.try_find("stop-sync").is_none());
        window.click("pause-transfers", cx);
    })
    .unwrap();
    wait_for(cx, &workspace, |view| view.progress.paused).await;
    cx.update(|cx| cx.dispatch_action(&crate::platform::Logout));
    wait_for(cx, &workspace, |view| {
        view.stopping.is_none() && !view.loaded
    })
    .await;
    assert!(!cx.update(|cx| workspace.read(cx).client.has_credentials()));
    cancel.cancel();
}

#[gpui_kit::test]
async fn logout_waits_for_admitted_work_to_drain(cx: &mut TestAppContext) {
    let profile = tempfile::tempdir().unwrap();
    let config = Config::load_in(None, profile.path().into()).unwrap();
    let client = Client::desktop(config).unwrap();
    let runtime = Arc::new(tokio::runtime::Runtime::new().unwrap());
    let tasks = TaskTracker::new();
    let lifetime = CancellationToken::new();
    cx.update(gpui_kit::init);
    cx.executor().allow_parking();
    let mut view = None;
    let window = cx.open_window(size(px(840.), px(600.)), |window, cx| {
        let entity = cx
            .new(|cx| Workspace::new(client, runtime.clone(), lifetime, tasks.clone(), window, cx));
        crate::platform::install_actions(&entity, cx);
        view = Some(entity.clone());
        Root::new(entity, window, cx)
    });
    let view = view.unwrap();
    cx.update_window(window.into(), |_, window, cx| window.render_frame(cx))
        .unwrap();
    cx.simulate_keystrokes(window.into(), "cmd-q");
    assert!(cx.update(|cx| view.read(cx).quit_notice.is_some()));
    assert!(cx.update(|cx| view.read(cx).stopping.is_none()));
    let cancel = cx.update(|cx| view.read(cx).cancel.clone());
    let (admitted, started) = tokio::sync::oneshot::channel();
    let (cancelled, cancellation) = tokio::sync::oneshot::channel();
    let (commit, committed) = tokio::sync::oneshot::channel();
    tasks.spawn_on(
        async move {
            admitted.send(()).unwrap();
            cancel.cancelled().await;
            cancelled.send(()).unwrap();
            committed.await.unwrap();
        },
        runtime.handle(),
    );
    started.await.unwrap();
    cx.update(|cx| cx.dispatch_action(&crate::platform::Logout));
    cancellation.await.unwrap();
    assert_eq!(tasks.len(), 1);
    assert!(cx.update(|cx| view.read(cx).stopping == Some(Shutdown::Logout)));
    // This gate represents the worker finishing its already-admitted commit.
    commit.send(()).unwrap();
    wait_for(cx, &view, |view| view.stopping.is_none()).await;
    assert!(tasks.is_empty());
    assert!(
        !profile.path().join("sync.sqlite").exists(),
        "authentication opened the sync journal"
    );
}

async fn wait_for(
    cx: &mut TestAppContext,
    view: &Entity<Workspace>,
    predicate: impl Fn(&Workspace) -> bool,
) {
    use futures_util::StreamExt;
    let mut notifications = cx.notifications(view);
    loop {
        if cx.update(|cx| predicate(view.read(cx))) {
            return;
        }
        notifications
            .next()
            .await
            .expect("workspace closed before its expected state");
    }
}
