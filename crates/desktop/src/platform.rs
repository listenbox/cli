use crate::workspace::{Shutdown, Workspace};
use gpui_kit::{App, Entity, Menu, MenuItem, Window, actions};

actions!(listenbox, [Logout, Quit]);

/// Query the actual shortcut key while its quit attempt is active. macOS can
/// consume Command-key releases before they reach GPUI's focused view.
pub fn quit_key_state() -> Option<Box<dyn Fn() -> bool>> {
    #[cfg(target_os = "macos")]
    {
        use objc2_app_kit::{NSApplication, NSEventType};
        use objc2_core_graphics::{CGEventFlags, CGEventSource, CGEventSourceStateID};
        let app = NSApplication::sharedApplication(objc2::MainThreadMarker::new()?);
        let event = app.currentEvent()?;
        if event.r#type() != NSEventType::KeyDown {
            return None;
        }
        // Use the event's hardware code, so this follows the active layout.
        let key = event.keyCode();
        Some(Box::new(move || {
            let state = CGEventSourceStateID::CombinedSessionState;
            CGEventSource::key_state(state, key)
                && CGEventSource::flags_state(state).contains(CGEventFlags::MaskCommand)
        }))
    }
    #[cfg(not(target_os = "macos"))]
    None
}

pub fn install(view: &Entity<Workspace>, window: &mut Window, cx: &mut App) {
    install_actions(view, cx);
    window.on_window_should_close(cx, |_, cx| {
        hide_window(cx);
        false
    });
    #[cfg(target_os = "macos")]
    macos::install_status_item(view, cx);
}

pub fn install_actions(view: &Entity<Workspace>, cx: &mut App) {
    let logout = view.clone();
    cx.on_action(move |_: &Logout, cx| {
        logout.update(cx, |view, cx| view.shutdown(Shutdown::Logout, cx))
    });
    let quit = view.clone();
    cx.on_action(move |_: &Quit, cx| quit.update(cx, |view, cx| view.shutdown(Shutdown::Quit, cx)));
    cx.set_menus(vec![Menu::new("Listenbox").items([
        MenuItem::action("Log out", Logout),
        MenuItem::separator(),
        MenuItem::action("Quit Listenbox", Quit),
    ])]);
}

pub fn show_window(cx: &mut App) {
    #[cfg(target_os = "macos")]
    macos::show_window();
    cx.activate(true);
}

fn hide_window(cx: &mut App) {
    #[cfg(target_os = "macos")]
    macos::hide_window();
    #[cfg(not(target_os = "macos"))]
    cx.hide();
    let _ = cx;
}

#[cfg(target_os = "macos")]
mod macos {
    use super::*;
    use gpui_kit::Global;
    use objc2_app_kit::{NSApplication, NSApplicationActivationPolicy};
    use tray_icon::{
        Icon, TrayIcon, TrayIconBuilder,
        menu::{Menu as TrayMenu, MenuEvent, MenuItem as TrayMenuItem},
    };

    struct StatusItem {
        _icon: TrayIcon,
    }
    impl Global for StatusItem {}

    pub(super) fn install_status_item(view: &Entity<Workspace>, cx: &mut App) {
        let menu = TrayMenu::new();
        let open = TrayMenuItem::new("Open Listenbox", true, None);
        let logout = TrayMenuItem::new("Log out", true, None);
        let quit = TrayMenuItem::new("Quit Listenbox", true, None);
        menu.append_items(&[&open, &logout, &quit])
            .expect("status menu");
        let (open, logout, quit) = (open.id().clone(), logout.id().clone(), quit.id().clone());
        let (sender, mut receiver) = tokio::sync::mpsc::unbounded_channel();
        MenuEvent::set_event_handler(Some(move |event: MenuEvent| {
            let _ = sender.send(event.id);
        }));
        let view = view.clone();
        cx.spawn(async move |cx| {
            while let Some(id) = receiver.recv().await {
                cx.update(|cx| {
                    if id == open {
                        super::show_window(cx);
                    } else if id == logout {
                        view.update(cx, |view, cx| view.shutdown(Shutdown::Logout, cx));
                    } else if id == quit {
                        view.update(cx, |view, cx| view.shutdown(Shutdown::Quit, cx));
                    }
                });
            }
        })
        .detach();
        // A small monochrome waveform, drawn as a macOS template image so the
        // system supplies the correct menu-bar color in light and dark appearances.
        let mut pixels = vec![0; 22 * 22 * 4];
        for (x, height) in [(3, 6), (7, 12), (11, 18), (15, 10), (19, 4)] {
            for y in (22 - height) / 2..(22 + height) / 2 {
                for dx in 0..2 {
                    pixels[(y * 22 + x + dx) * 4 + 3] = 255;
                }
            }
        }
        let icon = TrayIconBuilder::new()
            .with_menu(Box::new(menu))
            .with_tooltip("Listenbox — YouTube to podcast sync")
            .with_icon(Icon::from_rgba(pixels, 22, 22).expect("status icon pixels"))
            .with_icon_as_template(true)
            .build()
            .expect("Listenbox status icon");
        cx.set_global(StatusItem { _icon: icon });
    }

    pub(super) fn hide_window() {
        let app = NSApplication::sharedApplication(
            objc2::MainThreadMarker::new().expect("macOS main thread"),
        );
        for window in app.windows() {
            if window.title().to_string() == "Listenbox" {
                window.orderOut(None);
            }
        }
        app.setActivationPolicy(NSApplicationActivationPolicy::Accessory);
    }

    pub(super) fn show_window() {
        let app = NSApplication::sharedApplication(
            objc2::MainThreadMarker::new().expect("macOS main thread"),
        );
        app.setActivationPolicy(NSApplicationActivationPolicy::Regular);
        for window in app.windows() {
            if window.title().to_string() == "Listenbox" {
                window.deminiaturize(None);
                window.makeKeyAndOrderFront(None);
            }
        }
    }
}
