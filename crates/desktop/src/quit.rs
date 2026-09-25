//! Chrome-style protection for the keyboard shortcut. Native menu Quit is explicit.
use std::time::{Duration, Instant};

pub const DOUBLE_PRESS: Duration = Duration::from_secs(1);
pub const HOLD: Duration = Duration::from_secs(2);

#[derive(Default)]
pub struct QuitGuard {
    last_press: Option<Instant>,
    held_since: Option<Instant>,
    confirmed: bool,
}

impl QuitGuard {
    pub fn press(&mut self, now: Instant, repeat: bool) {
        if repeat || self.held_since.is_some() {
            return;
        }
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

    pub fn release(&mut self, now: Instant) -> bool {
        self.holding(now);
        self.held_since = None;
        self.confirmed
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
        assert!(!guard.release(now + Duration::from_millis(400)));
        guard.press(now + Duration::from_millis(500), false);
        assert!(guard.release(now + Duration::from_millis(550)));
        let mut hold = QuitGuard::default();
        hold.press(now, false);
        assert!(!hold.holding(now + Duration::from_secs(1)));
        assert!(hold.holding(now + HOLD));
        assert!(hold.release(now + HOLD));
    }

    #[test]
    fn expired_second_press_is_a_new_attempt() {
        let now = Instant::now();
        let mut guard = QuitGuard::default();
        guard.press(now, false);
        assert!(!guard.release(now));
        guard.press(now + DOUBLE_PRESS, false);
        assert!(!guard.release(now + DOUBLE_PRESS));
    }
}
