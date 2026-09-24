//! YouTube.js runs inside QuickJS; Rust owns cancellation and all HTTP transfers.
use crate::api::{Api, read_bounded};
use anyhow::{Context as _, Result, anyhow, bail, ensure};
use base64::{Engine, prelude::BASE64_STANDARD};
use llrt_modules::module_builder::ModuleBuilder;
use rquickjs::{
    AsyncContext, AsyncRuntime, Function, Module, Promise, async_with,
    function::{Async, Func},
};
use serde::Deserialize;
use serde_json::{Value, json};

pub struct YouTube {
    context: AsyncContext,
    _runtime: AsyncRuntime,
}

impl YouTube {
    pub async fn new(api: &Api) -> Result<Self> {
        let runtime = AsyncRuntime::new()?;
        runtime.set_memory_limit(256 * 1024 * 1024).await;
        runtime.set_max_stack_size(4 * 1024 * 1024).await;
        let cancel = api.cancel.clone();
        runtime
            .set_interrupt_handler(Some(Box::new(move || cancel.is_cancelled())))
            .await;
        let (resolver, loader, globals) = ModuleBuilder::default().build();
        runtime.set_loader(resolver, loader).await;
        let context = AsyncContext::full(&runtime).await?;
        let api = api.clone();
        context
            .with(|ctx| {
                use llrt_utils::primordials::Primordial;
                llrt_utils::primordials::BasePrimordials::init(&ctx)?;
                globals.attach(&ctx)?;
                ctx.globals()
                    .set("structuredClone", Func::from(clone_value))?;
                ctx.globals().set(
                    "hostFetch",
                    Func::from(Async(move |request: String| {
                        let api = api.clone();
                        async move {
                            match fetch(&api, &request).await {
                                Ok(value) => value.to_string(),
                                Err(error) => json!({"error": error.to_string()}).to_string(),
                            }
                        }
                    })),
                )?;
                ctx.eval::<(), _>(
                    "var console = {log(){},info(){},warn(){},error(){},debug(){}};",
                )?;
                let module =
                    Module::declare(ctx.clone(), "youtube", include_str!("../.cache/youtube.js"))?;
                let (module, evaluated) = module.eval()?;
                evaluated.finish::<()>()?;
                ctx.globals().set("YouTubeBridge", module.namespace()?)
            })
            .await
            .context("initialize YouTube.js")?;
        Ok(Self {
            context,
            _runtime: runtime,
        })
    }

    pub async fn call(&self, api: &Api, method: &str, args: Value) -> Result<Value> {
        let result = api
            .wait(async {
                let text = async_with!(self.context => |ctx| {
                    let bridge: rquickjs::Object = ctx.globals().get("YouTubeBridge")?;
                    let function: Function = bridge.get("call")?;
                    let promise: Promise = function.call((method, args.to_string()))?;
                    promise.into_future::<String>().await
                })
                .await
                .map_err(|error| anyhow!("YouTube.js: {error}"))?;
                Ok(serde_json::from_str::<Value>(&text)?)
            })
            .await?;
        if let Some(error) = result["bridge_error"].as_str() {
            bail!("YouTube: {error}");
        }
        Ok(result)
    }
}

fn clone_value<'js>(
    ctx: rquickjs::Ctx<'js>,
    value: rquickjs::Value<'js>,
    options: rquickjs::function::Opt<rquickjs::Object<'js>>,
) -> rquickjs::Result<rquickjs::Value<'js>> {
    llrt_utils::clone::structured_clone(&ctx, value, options)
}

#[derive(Deserialize)]
struct Fetch {
    url: String,
    method: String,
    headers: Vec<(String, String)>,
    body: Option<String>,
}

async fn fetch(api: &Api, request: &str) -> Result<Value> {
    let input: Fetch = serde_json::from_str(request)?;
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
        request = request.body(BASE64_STANDARD.decode(body)?);
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
        .collect::<std::collections::HashMap<_, _>>();
    let body = api.wait(read_bounded(response, 32 << 20)).await?;
    Ok(json!({"status": status, "headers": headers, "body": BASE64_STANDARD.encode(body)}))
}
