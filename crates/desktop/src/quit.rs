//! Chrome-style protection for the keyboard shortcut. Native menu Quit is explicit.
use std::time::{Duration, Instant};

pub const DOUBLE_PRESS: Duration = Duration::from_secs(1);
pub const HOLD: Duration = Duration::from_secs(2);
pub const NOTICE: Duration = Duration::from_secs(2);
pub const FADE: Duration = Duration::from_millis(200);
pub const KEY_POLL: Duration = Duration::from_millis(50);
pub const INSTRUCTION: &str = "Hold ⌘Q or press it again to quit";

#[derive(Clone, Copy)]
pub struct QuitNotice {
    shown_at: Instant,
    pub instruction: &'static str,
}

impl QuitNotice {
    pub fn new(now: Instant) -> Self {
        Self {
            shown_at: now,
            instruction: INSTRUCTION,
        }
    }

    pub fn opacity(self, now: Instant) -> f32 {
        let fading = now.duration_since(self.shown_at).saturating_sub(NOTICE);
        (1. - fading.as_secs_f32() / FADE.as_secs_f32()).clamp(0., 1.)
    }
}

#[derive(Default)]
pub struct QuitGuard {
    last_press: Option<Instant>,
    held_since: Option<Instant>,
    confirmed: bool,
}

impl QuitGuard {
    pub fn press(&mut self, now: Instant, repeat: bool) {
        if repeat {
            return;
        }
        // A fresh, non-repeat key-down also proves the previous press ended,
        // even if AppKit did not deliver its key-up to the focused view.
        self.confirmed = self
            .last_press
            .is_some_and(|last| now.duration_since(last) < DOUBLE_PRESS);
        self.last_press = Some(now);
        self.held_since = Some(now);
    }

    pub fn holding(&mut self, now: Instant) -> bool {
        self.confirmed |= self
            .held_since
            .is_some_and(|since| now.duration_since(since) >= HOLD);
        self.confirmed
    }

    pub fn release(&mut self) -> bool {
        if self.held_since.is_none() {
            return false;
        }
        self.held_since = None;
        std::mem::take(&mut self.confirmed)
    }

    pub fn is_held(&self) -> bool {
        self.held_since.is_some()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn quick_double_press_and_hold_quit_only_after_release() {
        let now = Instant::now();
        let mut guard = QuitGuard::default();
        guard.press(now, false);
        guard.press(now + Duration::from_millis(300), true);
        assert!(!guard.release());
        guard.press(now + Duration::from_millis(500), false);
        assert!(guard.release());
        assert!(
            !guard.release(),
            "a late duplicate release must be harmless"
        );
        let mut hold = QuitGuard::default();
        hold.press(now, false);
        assert!(!hold.holding(now + Duration::from_secs(1)));
        assert!(hold.holding(now + HOLD));
        assert!(hold.release());
    }

    #[test]
    fn expired_second_press_is_a_new_attempt() {
        let now = Instant::now();
        let mut guard = QuitGuard::default();
        guard.press(now, false);
        assert!(!guard.release());
        guard.press(now + DOUBLE_PRESS, false);
        assert!(!guard.release());
    }

    #[test]
    fn double_press_recovers_a_missing_first_release() {
        let now = Instant::now();
        let mut guard = QuitGuard::default();
        guard.press(now, false);
        guard.press(now + Duration::from_millis(300), false);
        assert!(guard.release());
        assert!(!guard.release());
    }
}
