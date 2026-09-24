Synthetic, two-second media fixtures copied from Listenbox's deterministic
YouTube E2E catalogue (listenbox/listenbox2, apps/fixtures/e2e/youtube).
No network or external executable is needed by the native media tests.

- video.mp4: dash-range/720/manifest-stream0.mp4, AVC 1280x720.
- aac.m4a: dash-range/720/manifest-stream1.mp4, AAC audio.
- opus.webm: audio/opus.webm, Opus audio.

Tests exercise AAC remux, Opus decode/resample/AAC encode, bounded two-input
muxing, fast-start MP4, HLS output, cancellation, and malformed input.
