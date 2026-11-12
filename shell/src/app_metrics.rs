use serde::Serialize;
use std::sync::Mutex;
use std::time::Instant;

#[derive(Default)]
pub struct AppMetricsState {
    previous: Mutex<Option<Sample>>,
}

struct Sample {
    at: Instant,
    cpu_time_ns: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AppMetrics {
    pub cpu_percent: Option<f64>,
    pub memory_bytes: u64,
    pub process_count: usize,
}

struct ProcessUsage {
    cpu_time_ns: u64,
    memory_bytes: u64,
}

impl AppMetricsState {
    pub fn sample(&self) -> Result<AppMetrics, String> {
        let processes = process_usages(std::process::id() as i32);
        if processes.is_empty() {
            return Err("application processes were not found".into());
        }
        let cpu_time_ns = processes.iter().map(|process| process.cpu_time_ns).sum();
        let memory_bytes = processes.iter().map(|process| process.memory_bytes).sum();
        let process_count = processes.len();
        let now = Instant::now();
        let mut previous = self.previous.lock().map_err(|_| "metrics lock failed")?;
        let cpu_percent = previous.as_ref().and_then(|sample| {
            cpu_percent(
                sample.cpu_time_ns,
                cpu_time_ns,
                now.duration_since(sample.at).as_nanos(),
            )
        });
        *previous = Some(Sample {
            at: now,
            cpu_time_ns,
        });
        Ok(AppMetrics {
            cpu_percent,
            memory_bytes,
            process_count,
        })
    }
}

fn cpu_percent(previous_ns: u64, current_ns: u64, elapsed_ns: u128) -> Option<f64> {
    if current_ns < previous_ns || elapsed_ns == 0 {
        return None;
    }
    Some((current_ns - previous_ns) as f64 / elapsed_ns as f64 * 100.0)
}

fn absolute_time_ns(time: u64, numer: u32, denom: u32) -> u64 {
    if denom == 0 {
        return 0;
    }
    ((time as u128 * numer as u128 / denom as u128).min(u64::MAX as u128)) as u64
}

#[cfg(target_os = "macos")]
fn process_usages(owner: i32) -> Vec<ProcessUsage> {
    use std::ffi::{c_int, c_void};
    use std::mem::{size_of, zeroed};
    use std::ptr::null_mut;

    #[repr(C)]
    struct RusageInfoV0 {
        _uuid: [u8; 16],
        user_time: u64,
        system_time: u64,
        _pkg_idle_wkups: u64,
        _interrupt_wkups: u64,
        _pageins: u64,
        _wired_size: u64,
        _resident_size: u64,
        phys_footprint: u64,
        _proc_start_abstime: u64,
        _proc_exit_abstime: u64,
    }

    #[repr(C)]
    struct MachTimebaseInfo {
        numer: u32,
        denom: u32,
    }

    unsafe extern "C" {
        fn mach_timebase_info(info: *mut MachTimebaseInfo) -> c_int;
        fn proc_listallpids(buffer: *mut c_void, buffersize: c_int) -> c_int;
        fn proc_pid_rusage(pid: c_int, flavor: c_int, buffer: *mut c_void) -> c_int;
        fn responsibility_get_pid_responsible_for_pid(pid: c_int) -> c_int;
    }

    let mut timebase = MachTimebaseInfo { numer: 0, denom: 0 };
    if unsafe { mach_timebase_info(&mut timebase) } != 0 || timebase.denom == 0 {
        return Vec::new();
    }
    let bytes = unsafe { proc_listallpids(null_mut(), 0) };
    if bytes <= 0 {
        return Vec::new();
    }
    let mut pids = vec![0_i32; bytes as usize / size_of::<i32>() + 16];
    let bytes = unsafe {
        proc_listallpids(
            pids.as_mut_ptr().cast(),
            (pids.len() * size_of::<i32>()) as c_int,
        )
    };
    if bytes <= 0 {
        return Vec::new();
    }
    pids.truncate((bytes as usize / size_of::<i32>()).min(pids.len()));
    pids.into_iter()
        .filter(|pid| {
            *pid == owner
                || (*pid > 0
                    && unsafe { responsibility_get_pid_responsible_for_pid(*pid) } == owner)
        })
        .filter_map(|pid| {
            let mut usage: RusageInfoV0 = unsafe { zeroed() };
            let result =
                unsafe { proc_pid_rusage(pid, 0, (&mut usage as *mut RusageInfoV0).cast()) };
            if result != 0 {
                return None;
            }
            Some(ProcessUsage {
                cpu_time_ns: absolute_time_ns(
                    usage.user_time.saturating_add(usage.system_time),
                    timebase.numer,
                    timebase.denom,
                ),
                memory_bytes: usage.phys_footprint,
            })
        })
        .collect()
}

#[cfg(not(target_os = "macos"))]
fn process_usages(_owner: i32) -> Vec<ProcessUsage> {
    Vec::new()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn calculates_cpu_from_cumulative_process_time() {
        assert_eq!(cpu_percent(100, 350, 1_000), Some(25.0));
        assert_eq!(cpu_percent(100, 100, 1_000), Some(0.0));
        assert_eq!(cpu_percent(100, 350, 0), None);
        assert_eq!(cpu_percent(350, 100, 1_000), None);
    }

    #[test]
    fn converts_mach_absolute_time_to_nanoseconds() {
        assert_eq!(absolute_time_ns(24, 125, 3), 1_000);
        assert_eq!(absolute_time_ns(100, 1, 1), 100);
        assert_eq!(absolute_time_ns(100, 1, 0), 0);
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn samples_the_current_process_and_its_children() {
        let state = AppMetricsState::default();
        let first = state.sample().expect("first sample");
        assert!(first.memory_bytes > 0);
        assert!(first.process_count > 0);
        assert_eq!(first.cpu_percent, None);
        let busy_until = Instant::now() + std::time::Duration::from_millis(5);
        while Instant::now() < busy_until {
            std::hint::spin_loop();
        }
        let second = state.sample().expect("second sample");
        assert!(second.cpu_percent.is_some_and(|value| value > 0.0));
        assert!(second.memory_bytes > 0);
    }
}
