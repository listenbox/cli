use crate::tokens::{self, Tokens};
use gpui_kit::component::{
    Disableable, Sizable,
    button::{Button, ButtonVariants},
    input::{Input, InputState},
    progress::Progress,
    spinner::Spinner,
};
use gpui_kit::prelude::FluentBuilder;
use gpui_kit::*;
use listenbox_sync_engine::{
    client::{Catalog, Client},
    downloads::{Phase, Snapshot},
    publicapi::Show,
    sync::Report,
};
use std::{collections::HashMap, sync::Arc};
use tokio::sync::mpsc::{UnboundedSender, unbounded_channel};
use tokio_util::{sync::CancellationToken, task::TaskTracker};

pub struct Workspace {
    client: Client,
    runtime: Arc<tokio::runtime::Runtime>,
    cancel: CancellationToken,
    lifetime: CancellationToken,
    tasks: TaskTracker,
    stopping: Option<Shutdown>,
    quit_guard: crate::quit::QuitGuard,
    quit_notice: Option<crate::quit::QuitNotice>,
    quit_task: Option<Task<()>>,
    focus: FocusHandle,
    sender: UnboundedSender<Message>,
    catalog: Catalog,
    loaded: bool,
    loading: bool,
    authenticating: bool,
    saving: bool,
    team: Option<String>,
    team_picker: bool,
    selected: Option<String>,
    source: Entity<InputState>,
    jobs: HashMap<String, CancellationToken>,
    reports: HashMap<String, String>,
    error: Option<String>,
    progress: Snapshot,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Shutdown {
    Quit,
    Logout,
}

enum Message {
    Drained(Shutdown, anyhow::Result<()>),
    Open(String),
    Catalog(anyhow::Result<Catalog>),
    Login(anyhow::Result<()>),
    Source(anyhow::Result<Show>),
    Report(String, Report),
    Finished(String, anyhow::Result<()>),
}

impl Workspace {
    pub fn new(
        client: Client,
        runtime: Arc<tokio::runtime::Runtime>,
        cancel: CancellationToken,
        tasks: TaskTracker,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        let (sender, mut receiver) = unbounded_channel();
        let source = cx.new(|cx| {
            InputState::new(window, cx).placeholder("https://www.youtube.com/playlist?list=…")
        });
        cx.spawn_in(window, async move |view, cx| {
            while let Some(message) = receiver.recv().await {
                if view
                    .update_in(cx, |view, window, cx| view.receive(message, window, cx))
                    .is_err()
                {
                    break;
                }
            }
        })
        .detach();
        let downloads = client.downloads();
        let mut changes = downloads.changes();
        cx.spawn(async move |view, cx| {
            while changes.changed().await.is_ok() {
                let snapshot = downloads.snapshot();
                if view
                    .update(cx, |view, cx| {
                        view.progress = snapshot;
                        cx.notify();
                    })
                    .is_err()
                {
                    break;
                }
            }
        })
        .detach();
        let mut view = Self {
            client,
            runtime,
            lifetime: cancel.clone(),
            cancel: cancel.child_token(),
            tasks,
            stopping: None,
            quit_guard: Default::default(),
            quit_notice: None,
            quit_task: None,
            focus: cx.focus_handle(),
            sender,
            catalog: Catalog::default(),
            loaded: false,
            loading: false,
            authenticating: false,
            saving: false,
            team: None,
            team_picker: false,
            selected: None,
            source,
            jobs: HashMap::new(),
            reports: HashMap::new(),
            error: None,
            progress: Snapshot::default(),
        };
        view.focus.focus(window, cx);
        if view.client.has_credentials() {
            view.reload(cx);
        }
        view
    }

    pub fn shutdown(&mut self, mode: Shutdown, cx: &mut Context<Self>) {
        if self.stopping.is_some() {
            return;
        }
        self.stopping = Some(mode);
        self.quit_notice = None;
        self.quit_task = None;
        self.quit_guard = Default::default();
        self.cancel.cancel();
        self.tasks.close();
        let (tasks, client, sender) =
            (self.tasks.clone(), self.client.clone(), self.sender.clone());
        // This join is outside the tracker it waits for. No task is aborted: each
        // worker finishes admitted file writes, FFmpeg cleanup and SQLite commits.
        self.runtime.spawn(async move {
            tasks.wait().await;
            let result = if mode == Shutdown::Logout {
                client.logout()
            } else {
                Ok(())
            };
            let _ = sender.send(Message::Drained(mode, result));
        });
        cx.notify();
    }

    fn quit_pressed(&mut self, key_down: Option<Box<dyn Fn() -> bool>>, cx: &mut Context<Self>) {
        let now = cx.background_executor().now();
        self.quit_guard.press(now, false);
        self.quit_notice = Some(crate::quit::QuitNotice::new(now));
        // One owned task per attempt: a fresh press cancels the previous timer.
        // The notice's lifetime never depends on delivery of a key-up event.
        self.quit_task = Some(cx.spawn(async move |view, cx| {
            loop {
                cx.background_executor().timer(crate::quit::KEY_POLL).await;
                let active = view
                    .update(cx, |view, cx| {
                        let now = cx.background_executor().now();
                        if view.quit_guard.is_held()
                            && let Some(key_down) = &key_down
                        {
                            if !key_down() {
                                view.quit_released(cx);
                            } else if view.quit_guard.holding(now)
                                && let Some(notice) = &mut view.quit_notice
                            {
                                notice.instruction = "Release ⌘Q to quit";
                            }
                        }
                        if view
                            .quit_notice
                            .is_some_and(|notice| notice.opacity(now) == 0.)
                        {
                            view.quit_notice = None;
                            // With no native key state, a missing release cannot leave
                            // a stale hold armed for an unrelated future key-up.
                            if key_down.is_none() {
                                view.quit_guard = Default::default();
                            }
                        }
                        cx.notify();
                        view.stopping.is_none()
                            && (view.quit_notice.is_some() || view.quit_guard.is_held())
                    })
                    .unwrap_or(false);
                if !active {
                    break;
                }
            }
        }));
        cx.notify();
    }

    fn quit_released(&mut self, cx: &mut Context<Self>) {
        if self.quit_guard.release() {
            self.shutdown(Shutdown::Quit, cx);
        }
    }

    fn reload(&mut self, cx: &mut Context<Self>) {
        if self.loading || self.stopping.is_some() {
            return;
        }
        self.loading = true;
        self.error = None;
        let (client, sender, cancel) = (
            self.client.clone(),
            self.sender.clone(),
            self.cancel.child_token(),
        );
        self.tasks.spawn_on(
            async move {
                let _ = sender.send(Message::Catalog(client.catalog(cancel).await));
            },
            self.runtime.handle(),
        );
        cx.notify();
    }

    fn login(&mut self, cx: &mut Context<Self>) {
        if self.authenticating || self.stopping.is_some() {
            return;
        }
        self.authenticating = true;
        self.error = None;
        let (client, sender, cancel) = (
            self.client.clone(),
            self.sender.clone(),
            self.cancel.child_token(),
        );
        self.tasks.spawn_on(
            async move {
                let result = client
                    .login(cancel, |url| {
                        let _ = sender.send(Message::Open(url.into()));
                    })
                    .await;
                let _ = sender.send(Message::Login(result));
            },
            self.runtime.handle(),
        );
        cx.notify();
    }

    fn receive(&mut self, message: Message, window: &mut Window, cx: &mut Context<Self>) {
        if self.stopping.is_some() && !matches!(message, Message::Drained(..)) {
            return;
        }
        match message {
            Message::Drained(mode, result) => {
                if mode == Shutdown::Quit {
                    cx.quit();
                    return;
                }
                self.cancel = self.lifetime.child_token();
                self.tasks.reopen();
                self.stopping = None;
                self.authenticating = false;
                self.loading = false;
                self.saving = false;
                self.jobs.clear();
                self.reports.clear();
                match result {
                    Ok(()) => {
                        self.catalog = Catalog::default();
                        self.loaded = false;
                        self.team = None;
                        self.team_picker = false;
                        self.select(None, window, cx);
                        self.progress = Snapshot::default();
                    }
                    Err(error) => self.error = Some(format!("Could not sign out. {error:#}")),
                }
            }
            Message::Open(url) => cx.open_url(&url),
            Message::Catalog(result) => {
                self.loading = false;
                match result {
                    Ok(catalog) => {
                        self.loaded = true;
                        self.catalog = catalog;
                        if !self
                            .catalog
                            .shows
                            .iter()
                            .any(|show| self.selected.as_ref() == Some(&show.slug))
                        {
                            self.select(
                                self.catalog.shows.first().map(|show| show.slug.clone()),
                                window,
                                cx,
                            );
                        }
                    }
                    Err(error) => {
                        if error.is::<listenbox_sync_engine::api::AuthenticationRequired>() {
                            self.loaded = false;
                            self.catalog = Catalog::default();
                            self.select(None, window, cx);
                        } else {
                            self.error = Some(format!("Could not load podcasts. {error:#}"));
                        }
                    }
                }
            }
            Message::Login(result) => {
                self.authenticating = false;
                match result {
                    Ok(()) => self.reload(cx),
                    Err(error) => self.error = Some(format!("Sign-in did not finish. {error:#}")),
                }
            }
            Message::Source(result) => {
                self.saving = false;
                match result {
                    Ok(show) => {
                        if let Some(existing) = self
                            .catalog
                            .shows
                            .iter_mut()
                            .find(|existing| existing.id == show.id)
                        {
                            *existing = show;
                        }
                        self.select(self.selected.clone(), window, cx);
                    }
                    Err(error) => {
                        self.error = Some(format!("Could not save the source. {error:#}"))
                    }
                }
            }
            Message::Report(slug, report) => {
                self.reports.insert(
                    slug,
                    format!(
                        "{} added · {} removed · {} unchanged{}",
                        report.added,
                        report.removed,
                        report.unchanged,
                        if report.reordered {
                            " · Order updated"
                        } else {
                            ""
                        }
                    ),
                );
            }
            Message::Finished(slug, result) => {
                let stopped = self
                    .jobs
                    .remove(&slug)
                    .is_some_and(|cancel| cancel.is_cancelled());
                if stopped {
                    self.reports
                        .insert(slug, "Stopped. Progress is saved for the next sync.".into());
                } else if let Err(error) = result {
                    self.reports
                        .insert(slug, format!("Sync paused after an error. {error:#}"));
                }
            }
        }
        cx.notify();
    }

    fn select(&mut self, slug: Option<String>, window: &mut Window, cx: &mut Context<Self>) {
        self.selected = slug;
        let value = self
            .show()
            .and_then(|show| show.youtube_source_url.clone())
            .unwrap_or_default();
        self.source
            .update(cx, |state, cx| state.set_value(value, window, cx));
        self.error = None;
        cx.notify();
    }

    fn show(&self) -> Option<&Show> {
        self.catalog
            .shows
            .iter()
            .find(|show| Some(&show.slug) == self.selected.as_ref())
    }

    fn save_source(&mut self, disconnect: bool, cx: &mut Context<Self>) {
        if self.stopping.is_some() {
            return;
        }
        let Some(show) = self.show() else {
            return;
        };
        let slug = show.slug.clone();
        let url = if disconnect {
            None
        } else {
            Some(self.source.read(cx).value().to_string())
        };
        self.saving = true;
        self.error = None;
        let (client, sender, cancel) = (
            self.client.clone(),
            self.sender.clone(),
            self.cancel.child_token(),
        );
        self.tasks.spawn_on(
            async move {
                let _ = sender.send(Message::Source(client.set_source(&slug, url, cancel).await));
            },
            self.runtime.handle(),
        );
        cx.notify();
    }

    fn sync(&mut self, watch: bool, cx: &mut Context<Self>) {
        if self.stopping.is_some() {
            return;
        }
        let Some(show) = self.show() else {
            return;
        };
        let slug = show.slug.clone();
        if self.jobs.contains_key(&slug) {
            return;
        }
        let cancel = self.cancel.child_token();
        self.jobs.insert(slug.clone(), cancel.clone());
        self.reports
            .insert(slug.clone(), "Reading YouTube and Listenbox…".into());
        let (client, sender) = (self.client.clone(), self.sender.clone());
        self.tasks.spawn_on(
            async move {
                let report_slug = slug.clone();
                let report_sender = sender.clone();
                let result = client
                    .sync(&slug, watch, cancel, move |report| {
                        let _ = report_sender
                            .send(Message::Report(report_slug.clone(), report.clone()));
                    })
                    .await;
                let _ = sender.send(Message::Finished(slug, result));
            },
            self.runtime.handle(),
        );
        cx.notify();
    }

    fn sidebar(&self, cx: &mut Context<Self>) -> AnyElement {
        let t = Tokens::current(cx);
        let team_name = self
            .team
            .as_ref()
            .and_then(|id| self.catalog.teams.iter().find(|team| &team.id == id))
            .map(|team| team.name.clone())
            .unwrap_or_else(|| "All teams".into());
        let mut rail = div()
            .flex()
            .flex_col()
            .w(px(tokens::SIDEBAR))
            .flex_shrink_0()
            .bg(t.rail)
            .border_r_1()
            .border_color(t.divider)
            .p(px(tokens::GAP))
            .gap(px(tokens::GAP))
            .child(
                div()
                    .px(px(tokens::GAP))
                    .py(px(tokens::GAP))
                    .text_size(px(tokens::TITLE))
                    .font_weight(FontWeight::BOLD)
                    .child("Listenbox"),
            )
            .child(
                Button::new("team-picker")
                    .label(team_name)
                    .icon(gpui_kit::component::IconName::ChevronDown)
                    .w_full()
                    .disabled(!self.loaded)
                    .on_click(cx.listener(|view, _, _, cx| {
                        view.team_picker = !view.team_picker;
                        cx.notify();
                    })),
            );
        if self.team_picker {
            rail = rail.child(
                Button::new("all-teams")
                    .ghost()
                    .label("All teams")
                    .on_click(cx.listener(|view, _, _, cx| {
                        view.team = None;
                        view.team_picker = false;
                        cx.notify();
                    })),
            );
            for team in &self.catalog.teams {
                let id = team.id.clone();
                rail = rail.child(
                    Button::new(SharedString::from(format!("team-{}", team.id)))
                        .ghost()
                        .label(team.name.clone())
                        .on_click(cx.listener(move |view, _, window, cx| {
                            view.team = Some(id.clone());
                            view.team_picker = false;
                            let slug = view
                                .catalog
                                .shows
                                .iter()
                                .find(|show| show.team_id == id)
                                .map(|show| show.slug.clone());
                            view.select(slug, window, cx);
                        })),
                );
            }
        }
        let mut shows = div()
            .id("podcasts")
            .flex()
            .flex_col()
            .gap_1()
            .flex_1()
            .min_h_0()
            .overflow_y_scroll();
        for show in self
            .catalog
            .shows
            .iter()
            .filter(|show| self.team.as_ref().is_none_or(|team| team == &show.team_id))
        {
            let slug = show.slug.clone();
            let selected = self.selected.as_ref() == Some(&slug);
            shows = shows.child(
                Button::new(SharedString::from(format!("show-{slug}")))
                    .ghost()
                    .label(show.title.clone())
                    .w_full()
                    .justify_start()
                    .when(selected, |button| button.bg(t.selected))
                    .on_click(cx.listener(move |view, _, window, cx| {
                        view.select(Some(slug.clone()), window, cx)
                    })),
            );
        }
        rail.child(shows)
            .child(
                div()
                    .p(px(tokens::GAP))
                    .text_size(px(12.))
                    .text_color(t.muted)
                    .child("YouTube → Listenbox"),
            )
            .into_any_element()
    }

    fn detail(&self, cx: &mut Context<Self>) -> AnyElement {
        let t = Tokens::current(cx);
        let Some(show) = self.show() else {
            return div().flex().flex_col().items_start().gap(px(tokens::GAP)).py(px(tokens::SPACE * 2.)).child(div().text_size(px(tokens::PAGE_TITLE)).font_weight(FontWeight::BOLD).child("Connect your podcast"))
                .child(div().max_w(px(510.)).text_color(t.muted).child(if self.loaded { "Create a podcast in Listenbox, or ask a team owner to share one with you. Then reload your podcasts here." } else { "Sign in through Listenbox in your browser, then choose a podcast and its YouTube playlist. Syncing requires a paid audio or video plan." }))
                .child(Button::new("welcome-action").primary().label(if self.loaded { "Open Listenbox" } else { "Sign in to Listenbox" }).disabled(self.authenticating || self.stopping.is_some()).on_click(cx.listener(|view, _, _, cx| { if view.loaded { cx.open_url(view.client.dashboard_url()); } else { view.login(cx); } }))).into_any_element();
        };
        let running = self.jobs.contains_key(&show.slug);
        let disabled = running
            || self.saving
            || self.stopping.is_some()
            || !show.has_active_subscription
            || show.youtube_destination;
        let sync_disabled = disabled || show.youtube_source_url.is_none();
        let mut pane = div().flex().flex_col().gap(px(tokens::SPACE))
            .child(div().flex().items_start().justify_between().gap_4()
                .child(div().flex_1().min_w_0().child(div().text_size(px(tokens::PAGE_TITLE)).font_weight(FontWeight::BOLD).child(show.title.clone())).child(div().mt_2().text_color(t.muted).child(format!("{} podcast", if show.source_kind == listenbox_sync_engine::publicapi::ShowSourceKind::Audio { "Audio" } else { "Video" }))))
                .child(Button::new("open-show").ghost().label("Open in Listenbox").on_click(cx.listener(|view, _, _, cx| { if let Some(url) = view.show().and_then(|show| view.client.show_url(show).ok()) { cx.open_url(&url); } }))))
            .child(div().flex().flex_col().gap_2().child(div().font_weight(FontWeight::SEMIBOLD).child("YouTube playlist"))
                .child(Input::new(&self.source).id("playlist-url").disabled(disabled))
                .child(div().text_color(t.muted).text_size(px(12.)).child("New videos become episodes in playlist order. Videos removed from this playlist are removed from this podcast; other episodes stay."))
                .child(div().flex().gap_2().mt_2()
                    .child(Button::new("save-source").label(if self.saving { "Saving…" } else { "Save playlist" }).disabled(disabled).on_click(cx.listener(|view, _, _, cx| view.save_source(false, cx))))
                    .child(Button::new("disconnect").ghost().label("Disconnect").disabled(disabled || show.youtube_source_url.is_none()).on_click(cx.listener(|view, _, _, cx| view.save_source(true, cx))))));
        if show.youtube_destination {
            pane = pane.child(div().text_color(t.muted).child("This podcast publishes to YouTube. Disconnect its YouTube destination in Listenbox before adding a source."));
        }
        if !show.has_active_subscription {
            pane =
                pane.child(
                    div()
                        .flex()
                        .flex_col()
                        .gap_2()
                        .child("A paid audio or video plan is required to sync this podcast.")
                        .child(Button::new("choose-plan").label("Open billing").on_click(
                            cx.listener(|view, _, _, cx| {
                                cx.open_url(view.client.dashboard_url());
                            }),
                        )),
                );
        }
        pane = pane
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(
                        Button::new("sync-now")
                            .primary()
                            .label("Sync now")
                            .disabled(sync_disabled)
                            .on_click(cx.listener(|view, _, _, cx| view.sync(false, cx))),
                    )
                    .child(
                        Button::new("keep-syncing")
                            .label("Keep syncing")
                            .disabled(sync_disabled)
                            .on_click(cx.listener(|view, _, _, cx| view.sync(true, cx))),
                    )
                    .when(running, |row| {
                        row.child(Button::new("stop-sync").label("Stop").on_click(cx.listener(
                            |view, _, _, cx| {
                                if let Some(cancel) =
                                    view.selected.as_ref().and_then(|slug| view.jobs.get(slug))
                                {
                                    cancel.cancel();
                                }
                                cx.notify();
                            },
                        )))
                    }),
            )
            .child(div().text_color(t.muted).child(
                self.reports.get(&show.slug).cloned().unwrap_or_else(|| {
                    "Ready when you are. Keep syncing checks for changes every hour.".into()
                }),
            ));
        pane.into_any_element()
    }

    fn transfers(&self, cx: &mut Context<Self>) -> AnyElement {
        let t = Tokens::current(cx);
        let mut rows = div()
            .flex()
            .flex_col()
            .gap(px(tokens::GAP))
            .mt(px(tokens::SPACE))
            .border_t_1()
            .border_color(t.divider)
            .pt(px(tokens::SPACE))
            .child(
                div()
                    .flex()
                    .justify_between()
                    .items_center()
                    .child(
                        div()
                            .text_size(px(tokens::TITLE))
                            .font_weight(FontWeight::SEMIBOLD)
                            .child("Transfers"),
                    )
                    .child(
                        Button::new("pause-transfers")
                            .ghost()
                            .label(if self.progress.paused {
                                "Resume queue"
                            } else {
                                "Pause queue"
                            })
                            .disabled(self.progress.items.is_empty())
                            .on_click(cx.listener(|view, _, _, cx| {
                                view.client.downloads().set_paused(!view.progress.paused);
                                cx.notify();
                            })),
                    ),
            );
        if self.progress.items.is_empty() {
            rows = rows.child(
                div()
                    .py(px(tokens::SPACE))
                    .text_color(t.muted)
                    .child("Downloads and uploads will appear here when you start syncing."),
            );
        }
        for item in self.progress.items.iter().rev().take(100) {
            let mut row = div()
                .flex()
                .flex_col()
                .gap_2()
                .py_3()
                .border_b_1()
                .border_color(t.divider)
                .child(
                    div()
                        .flex()
                        .justify_between()
                        .gap_4()
                        .child(
                            div().flex_1().min_w_0().child(item.title.clone()).child(
                                div()
                                    .text_size(px(12.))
                                    .text_color(t.muted)
                                    .child(item.source_title.clone()),
                            ),
                        )
                        .child(
                            div()
                                .text_color(if item.phase == Phase::Failed {
                                    t.danger
                                } else {
                                    t.muted
                                })
                                .child(item.phase.label()),
                        ),
                );
            if item.phase == Phase::Downloading {
                let value = if item.total == 0 {
                    0.
                } else {
                    100. * item.received() as f32 / item.total as f32
                };
                row = row
                    .child(
                        Progress::new(SharedString::from(format!("progress-{}", item.id)))
                            .value(value)
                            .accessibility_label(format!("Downloading {}", item.title)),
                    )
                    .child(div().text_size(px(12.)).text_color(t.muted).child(format!(
                        "{:.1} / {:.1} MB · {:.1} MB/s",
                        item.received() as f64 / 1_000_000.,
                        item.total as f64 / 1_000_000.,
                        item.bytes_per_second() as f64 / 1_000_000.
                    )));
            }
            if let Some(error) = &item.error {
                row = row.child(div().text_color(t.danger).child(error.clone()));
            }
            rows = rows.child(row);
        }
        rows.into_any_element()
    }
}

impl Render for Workspace {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let t = Tokens::current(cx);
        let quit_notice = if self.stopping.is_some() {
            Some(("Finishing current work…", 1.))
        } else {
            self.quit_notice.map(|notice| {
                let mut opacity = notice.opacity(cx.background_executor().now());
                if opacity > 0. && opacity < 1. {
                    if cx.reduce_motion() {
                        opacity = 0.;
                    } else {
                        window.request_animation_frame();
                    }
                }
                (notice.instruction, opacity)
            })
        };
        div()
            .track_focus(&self.focus)
            .capture_key_down(cx.listener(|view, event: &KeyDownEvent, _, cx| {
                if event.keystroke.modifiers.platform && event.keystroke.key == "q" {
                    cx.stop_propagation();
                    if event.is_held || view.stopping.is_some() {
                        return;
                    }
                    view.quit_pressed(crate::platform::quit_key_state(), cx);
                }
            }))
            .capture_key_up(cx.listener(|view, event: &KeyUpEvent, _, cx| {
                if event.keystroke.key == "q" {
                    view.quit_released(cx);
                }
            }))
            .on_modifiers_changed(cx.listener(|view, event: &ModifiersChangedEvent, _, cx| {
                if !event.modifiers.platform {
                    view.quit_released(cx);
                }
            }))
            .relative()
            .size_full()
            .flex()
            .bg(t.background)
            .text_color(t.ink)
            .text_size(px(tokens::BODY))
            .child(self.sidebar(cx))
            .child(
                div()
                    .flex()
                    .flex_col()
                    .flex_1()
                    .min_w_0()
                    .child(
                        div()
                            .h(px(56.))
                            .px(px(tokens::SPACE))
                            .flex()
                            .items_center()
                            .justify_between()
                            .border_b_1()
                            .border_color(t.divider)
                            .child(div().text_color(t.muted).child("Podcasts"))
                            .child(
                                div()
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .when(self.loading || self.authenticating, |row| {
                                        row.child(Spinner::new().small())
                                    })
                                    .child(
                                        Button::new("reload")
                                            .ghost()
                                            .label("Reload")
                                            .disabled(self.loading || self.stopping.is_some())
                                            .on_click(
                                                cx.listener(|view, _, _, cx| view.reload(cx)),
                                            ),
                                    )
                                    .child(
                                        Button::new("sign-in")
                                            .ghost()
                                            .label(if self.authenticating {
                                                "Finish in your browser…"
                                            } else if self.loaded {
                                                "Log out"
                                            } else {
                                                "Sign in"
                                            })
                                            .disabled(
                                                self.authenticating || self.stopping.is_some(),
                                            )
                                            .on_click(cx.listener(|view, _, _, cx| {
                                                if view.loaded {
                                                    view.shutdown(Shutdown::Logout, cx);
                                                } else {
                                                    view.login(cx);
                                                }
                                            })),
                                    ),
                            ),
                    )
                    .child(
                        div()
                            .id("workspace-content")
                            .flex_1()
                            .min_h_0()
                            .overflow_y_scroll()
                            .p(px(tokens::SPACE))
                            .child(self.detail(cx))
                            .when_some(self.error.clone(), |pane, error| {
                                pane.child(div().mt_4().text_color(t.danger).child(error))
                            })
                            .when(self.loaded, |pane| pane.child(self.transfers(cx))),
                    ),
            )
            .when_some(quit_notice, |workspace, (instruction, opacity)| {
                workspace.child(
                    div()
                        .absolute()
                        .inset_0()
                        .flex()
                        .items_center()
                        .justify_center()
                        .child(
                            div()
                                .id("quit-notice")
                                .opacity(opacity)
                                .occlude()
                                .w(px(tokens::QUIT_HUD_WIDTH))
                                .p(px(tokens::SPACE))
                                .rounded(px(tokens::QUIT_HUD_RADIUS))
                                .bg(t.action.opacity(0.96))
                                .text_color(t.action_ink)
                                .flex()
                                .flex_col()
                                .items_center()
                                .gap(px(tokens::GAP))
                                .text_center()
                                .child(
                                    div()
                                        .text_size(px(if self.stopping.is_some() {
                                            tokens::TITLE
                                        } else {
                                            tokens::QUIT_SHORTCUT
                                        }))
                                        .line_height(relative(1.))
                                        .font_weight(FontWeight::MEDIUM)
                                        .child(if self.stopping.is_some() {
                                            "Saving progress"
                                        } else {
                                            "⌘ Q"
                                        }),
                                )
                                .child(div().child(instruction)),
                        ),
                )
            })
    }
}

impl Drop for Workspace {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}

#[cfg(test)]
mod tests;
