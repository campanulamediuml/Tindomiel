#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use serde::{Deserialize, Serialize};
use std::fs;
use std::io::{BufRead, BufReader};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use tauri::{Manager, RunEvent, State, WebviewWindowBuilder};

#[derive(Clone, Deserialize, Serialize)]
struct BackendInfo {
    url: String,
    token: String,
}

struct BackendState {
    info: BackendInfo,
    child: Mutex<Option<Child>>,
}

#[tauri::command]
fn get_backend_info(state: State<'_, BackendState>) -> BackendInfo {
    state.info.clone()
}

fn main() {
    let app = tauri::Builder::default()
        .setup(|app| {
            let state = start_backend(app)?;
            let mut config = app
                .config()
                .app
                .windows
                .first()
                .cloned()
                .ok_or("main window config is missing")?;
            if let Some(monitor) = app.primary_monitor()? {
                config.height =
                    (f64::from(monitor.size().height) / monitor.scale_factor() * 0.8).round();
            }
            WebviewWindowBuilder::from_config(app, &config)?.build()?;
            app.manage(state);
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![get_backend_info])
        .build(tauri::generate_context!())
        .expect("failed to build Tindomiel");

    app.run(|app, event| {
        if let RunEvent::Exit = event {
            if let Some(state) = app.try_state::<BackendState>() {
                if let Ok(mut slot) = state.child.lock() {
                    if let Some(mut child) = slot.take() {
                        let _ = child.kill();
                        let _ = child.wait();
                    }
                }
            }
        }
    });
}

fn start_backend(app: &tauri::App) -> Result<BackendState, Box<dyn std::error::Error>> {
    let executable = backend_executable(app)?;
    let data_path = data_path()?;
    if let Some(parent) = data_path.parent() {
        fs::create_dir_all(parent)?;
    }
    let mut child = Command::new(executable)
        .env("TINDOMIEL_ADDR", "127.0.0.1:0")
        .env("TINDOMIEL_DATA", data_path)
        .stdout(Stdio::piped())
        .stderr(Stdio::inherit())
        .spawn()?;
    let stdout = child.stdout.take().ok_or("missing backend stdout")?;
    let mut reader = BufReader::new(stdout);
    let mut line = String::new();
    if reader.read_line(&mut line)? == 0 {
        let _ = child.kill();
        return Err("backend exited before reporting its address".into());
    }
    let info: BackendInfo = match serde_json::from_str(&line) {
        Ok(info) => info,
        Err(error) => {
            let _ = child.kill();
            return Err(format!("invalid backend startup response: {error}").into());
        }
    };
    std::thread::spawn(move || {
        let mut reader = reader;
        let mut line = String::new();
        loop {
            line.clear();
            match reader.read_line(&mut line) {
                Ok(0) | Err(_) => break,
                Ok(_) => eprint!("{line}"),
            }
        }
    });
    Ok(BackendState {
        info,
        child: Mutex::new(Some(child)),
    })
}

fn backend_executable(app: &tauri::App) -> Result<PathBuf, Box<dyn std::error::Error>> {
    if let Some(path) = std::env::var_os("TINDOMIEL_SERVER") {
        return Ok(PathBuf::from(path));
    }
    if cfg!(debug_assertions) {
        let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("resources")
            .join("tindomiel-server");
        if path.exists() {
            return Ok(path);
        }
        return Err("run npm run build:server before starting the desktop app".into());
    }
    Ok(app.path().resource_dir()?.join("tindomiel-server"))
}

fn data_path() -> Result<PathBuf, Box<dyn std::error::Error>> {
    if let Some(path) = std::env::var_os("TINDOMIEL_DATA") {
        return Ok(PathBuf::from(path));
    }
    let home = std::env::var_os("HOME").ok_or("HOME is not set")?;
    Ok(PathBuf::from(home)
        .join("Library")
        .join("Application Support")
        .join("Tindomiel")
        .join("profiles.json"))
}
