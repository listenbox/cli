use std::sync::{
    Arc,
    atomic::{AtomicBool, Ordering},
};
use youtubei::Engine;

#[tokio::test(flavor = "current_thread")]
async fn cancellation_interrupts_javascript_execution() -> youtubei::Result<()> {
    let engine = Engine::new().await?;
    let interrupted = Arc::new(AtomicBool::new(false));
    let observed = interrupted.clone();
    engine
        .set_interrupt_handler(move || {
            observed.store(true, Ordering::SeqCst);
            true
        })
        .await;
    let error = engine
        .value_with(|ctx| ctx.eval::<youtubei::rquickjs::Value, _>("while (true) {}"))
        .await
        .unwrap_err();
    assert!(interrupted.load(Ordering::SeqCst));
    assert!(error.to_string().contains("interrupted"), "{error}");
    Ok(())
}
