import json
import os
import platform
import shutil
import subprocess
import sys
from datetime import datetime
from pathlib import Path
from urllib.request import urlopen

import questionary
from questionary import Style
from rich import box
from rich.console import Console
from rich.panel import Panel
from rich.table import Table
from rich.text import Text

# ── Configuration ─────────────────────────────────────────────────────────────

IGNITE_HOME = Path.home() / ".ignite"

console = Console()

Q_STYLE = Style(
    [
        ("qmark", "fg:#7c6f64 bold"),
        ("question", "bold"),
        ("answer", "fg:#b8bb26 bold"),
        ("pointer", "fg:#fe8019 bold"),
        ("highlighted", "fg:#fe8019 bold"),
        ("selected", "fg:#b8bb26"),
        ("separator", "fg:#665c54"),
        ("instruction", "fg:#7c6f64"),
    ]
)

# ── Manager registry ──────────────────────────────────────────────────────────

MANAGERS: dict[str, dict] = {
    "foundry": {
        "label": "Foundry",
        "detect": lambda p: (
            (p / "foundry.toml").exists() or (p / "forge.toml").exists()
        ),
        "image": "touchmeangel/ignite:latest",
    }
}

# ── Provider / model registry ──────────────────────────────────────────────────

PROVIDERS = {
    "anthropic": {
        "label": "Anthropic (Claude)",
        "models": [
            "claude-sonnet-4-5",
            "claude-opus-4",
            "claude-opus-4-5",
            "claude-haiku-3-5",
        ],
        "env_key": "ANTHROPIC_API_KEY",
        "provider_str": "anthropic",
        "base_url": None,
    },
    "openai": {
        "label": "OpenAI (GPT / o-series)",
        "models": [
            "gpt-4.1",
            "o3",
            "o4-mini",
            "gpt-4.1-mini",
            "gpt-4o",
        ],
        "env_key": "OPENAI_API_KEY",
        "provider_str": "openai",
        "base_url": None,
    },
    "google": {
        "label": "Google (Gemini)",
        "models": [
            "gemini-2.5-pro",
            "gemini-2.5-flash",
            "gemini-2.0-flash",
        ],
        "env_key": "GOOGLE_API_KEY",
        "provider_str": "google",
        "base_url": "https://generativelanguage.googleapis.com/v1beta/openai/",
    },
    "ollama": {
        "label": "Ollama (Local Engine)",
        "models": [],
        "env_key": None,
        "provider_str": "ollama",
        "base_url": "http://localhost:11434",
    },
}

FALLBACK_OLLAMA_MODELS = [
    "qwen2.5-coder:14b",
    "qwen2.5-coder:7b",
    "deepseek-coder:33b",
    "deepseek-coder:6.7b",
    "llama3.1:8b",
    "codellama:13b",
]

# ── Detection helpers ──────────────────────────────────────────────────────────


def docker_running() -> bool:
    docker_path = shutil.which("docker")
    if not docker_path:
        return False
    try:
        subprocess.run(  # noqa: S603
            [docker_path, "info"], capture_output=True, check=True, timeout=10
        )
        return True
    except Exception:
        return False


def get_ollama_models(base_url: str = "http://localhost:11434") -> list[str]:
    if not (base_url.startswith("http://") or base_url.startswith("https://")):
        return []
    try:
        with urlopen(f"{base_url}/api/tags", timeout=4) as r:  # noqa: S310
            return [m["name"] for m in json.loads(r.read()).get("models", [])]
    except Exception:
        return []


# ── Interactive setup ──────────────────────────────────────────────────────────


def _ask_model_profile(role_name: str) -> tuple[str, str, str | None]:
    provider_label = questionary.select(
        f"Select provider for [{role_name.upper()}] engine:",
        choices=[p["label"] for p in PROVIDERS.values()],
        style=Q_STYLE,
    ).ask()
    if provider_label is None:
        sys.exit(0)

    prov_key = next(k for k, v in PROVIDERS.items() if v["label"] == provider_label)
    prov = PROVIDERS[prov_key]

    if prov_key == "ollama":
        base_url = (
            questionary.text(
                "Ollama endpoint address:", default=prov["base_url"], style=Q_STYLE
            ).ask()
            or prov["base_url"]
        )

        # Determine accessible Docker routing override context
        container_url = base_url
        if "localhost" in base_url or "127.0.0.1" in base_url:
            container_url = "http://host.docker.internal:11434"

        models = get_ollama_models(base_url)
        choices = (models or FALLBACK_OLLAMA_MODELS) + ["[ enter manually ]"]

        model = questionary.select(
            "Select model tag:", choices=choices, style=Q_STYLE
        ).ask()
        if model == "[ enter manually ]" or model is None:
            model = (
                questionary.text("Model signature identification:").ask()
                or FALLBACK_OLLAMA_MODELS[0]
            )
        return prov["provider_str"], model, container_url

    # Cloud Providers Selection List Pipeline
    model = questionary.select(
        "Select specific model variant:",
        choices=prov["models"],
        style=Q_STYLE,
    ).ask()
    if model is None:
        sys.exit(0)

    return prov["provider_str"], model, prov.get("base_url")


def _ask_api_key(provider_str: str) -> str:
    prov = next(
        (p for p in PROVIDERS.values() if p["provider_str"] == provider_str), {}
    )
    env_key = prov.get("env_key")
    if not env_key:
        return ""

    key = questionary.password(f"{env_key}:", style=Q_STYLE).ask() or ""
    if not key:
        console.print(
            f"  [yellow]⚠[/yellow]  Value omitted — initialize {env_key} inside environment values later"
        )
    return key


def run_setup(manager: str) -> dict:
    config_path = IGNITE_HOME / "config.json"
    env_path = IGNITE_HOME / ".env"
    IGNITE_HOME.mkdir(parents=True, exist_ok=True)

    # ── STEP 1: Flash Configuration ──
    console.print()
    console.rule(
        "[bold cyan]STEP 1 — Flash Model (Fast routing tasks, sub-scans)[/bold cyan]"
    )
    f_provider, f_model, f_base_url = _ask_model_profile("flash")

    # ── STEP 2: Reasoning Configuration ──
    console.print()
    console.rule(
        "[bold magenta]STEP 2 — Reasoning Model (Deep analysis, comprehensive logic reviews)[/bold magenta]"
    )
    r_provider, r_model, r_base_url = _ask_model_profile("reasoning")

    # ── STEP 3: API Pipeline Key Management ──
    console.print()
    console.rule(
        "[bold yellow]STEP 3 — Security Credentials & Token Storage[/bold yellow]"
    )

    collected_keys = {}
    active_cloud_providers = {f_provider, r_provider} - {"ollama"}

    for provider_id in active_cloud_providers:
        secret_token = _ask_api_key(provider_id)
        if secret_token:
            collected_keys[provider_id] = secret_token

    flash_entry: dict = {
        "provider": f_provider,
        "model": f_model,
        "temperature": 0.1,
        "max_tokens": 4096,
    }
    if f_base_url:
        flash_entry["base_url"] = f_base_url

    reasoning_entry: dict = {
        "provider": r_provider,
        "model": r_model,
        "temperature": 0.3,
        "max_tokens": 8192,
    }
    if r_base_url:
        reasoning_entry["base_url"] = r_base_url

    config = {
        "models": {
            "flash": flash_entry,
            "reasoning": reasoning_entry,
        },
        "task_routing": {
            "default": "flash",
            "deep_reasoning": "reasoning",
        },
    }

    config_path.write_text(json.dumps(config, indent=2))
    console.print(f"\n  [green]✔[/green]  config.json  →  {config_path}")

    env_lines = ["# Generated by ignite — do not commit", ""]
    for provider_id, token_value in collected_keys.items():
        matched_prov = next(
            (p for p in PROVIDERS.values() if p["provider_str"] == provider_id), {}
        )
        if matched_prov.get("env_key"):
            env_lines.append(f"{matched_prov['env_key']}={token_value}")

    env_path.write_text("\n".join(env_lines) + "\n")
    console.print(f"  [green]✔[/green]  .env         →  {env_path}")

    return config


# ── Docker Execution ──────────────────────────────────────────────────────────


def load_env_vars() -> dict[str, str]:
    env_path = IGNITE_HOME / ".env"
    skip = {"PROJECT_PATH"}
    env: dict[str, str] = {}
    if env_path.exists():
        for line in env_path.read_text().splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, _, v = line.partition("=")
                if k not in skip and v:
                    env[k] = v
    for key in ("ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY"):
        if key in os.environ and key not in env:
            env[key] = os.environ[key]
    return env


def pull_image(manager: str):
    docker_path = shutil.which("docker") or "docker"
    image = MANAGERS[manager]["image"]
    console.print(f"  [dim]Pulling latest image: {image} …[/dim]")
    subprocess.run([docker_path, "pull", image])  # noqa: S603, S607
    console.print("  [green]✔[/green]  Image updated.")


def run_container(manager: str, project_path: Path, config_path: Path) -> int:
    docker_path = shutil.which("docker") or "docker"
    image = MANAGERS[manager]["image"]
    env_vars = load_env_vars()

    extra_hosts = (
        ["--add-host", "host.docker.internal:host-gateway"]
        if platform.system() == "Linux"
        else []
    )

    env_flags = []
    for k, v in env_vars.items():
        env_flags += ["-e", f"{k}={v}"]

    user_mapping = []
    if platform.system() != "Windows":
        user_mapping = ["--user", f"{os.getuid()}:{os.getgid()}"]

    # Base Docker command configuration
    cmd = [
        docker_path,
        "run",
        "--rm",
        "-v",
        f"{project_path}:/project",
        "-v",
        f"{config_path}:/app/config.json:ro",
        *user_mapping,
        "-e",
        "PROJECT_PATH=/project",
        *env_flags,
        *extra_hosts,
        image,
        "--project-path",
        "/project",
        "--output",
        "/project/audit_results.json",
    ]
    console.print(f"\n  [dim]$ {' '.join(cmd)}[/dim]\n")
    return subprocess.run(cmd).returncode  # noqa: S603, S607


# ── Report display ─────────────────────────────────────────────────────────────

SEVERITY_COLOR = {
    "high": "bold red",
    "medium": "bold yellow",
    "low": "bold cyan",
    "informational": "dim",
}


def print_report(project_path: Path):
    results_path = project_path / "audit_results.json"
    report_path = project_path / "audit_report.md"

    if not results_path.exists():
        console.print("  [yellow]⚠[/yellow]  audit_results.json not found")
        return

    data = json.loads(results_path.read_text())

    buckets: dict[str, list] = {
        "high": [],
        "medium": [],
        "low": [],
        "informational": [],
    }
    for step_data in data.get("steps", {}).values():
        for sev in buckets:
            buckets[sev].extend(step_data.get("findings", {}).get(sev, []))

    total = sum(len(v) for v in buckets.values())
    h, m = len(buckets["high"]), len(buckets["medium"])

    project_name = project_path.name
    date_str = datetime.now().strftime("%Y-%m-%d")
    header_text = Text.assemble(
        ("SECURITY AUDIT REPORT\n", "bold"),
        (f"Project: {project_name}   ", ""),
        (f"Date: {date_str}\n", "dim"),
        (f"{h} High  ·  {m} Medium  ·  {total} total", "bold"),
    )
    console.print(Panel(header_text, box=box.DOUBLE_EDGE, padding=(0, 2)))

    if total == 0:
        console.print("\n  [bold green]No findings detected.[/bold green]\n")
        return

    for sev, items in buckets.items():
        if not items:
            continue
        color = SEVERITY_COLOR.get(sev, "")
        prefix = sev[0].upper()
        label = sev.upper()

        console.print(
            f"\n  [{color}]● {label}[/{color}]  ({len(items)} finding{'s' if len(items) != 1 else ''})\n"
        )

        tbl = Table(
            box=box.SIMPLE,
            show_header=True,
            header_style="dim",
            padding=(0, 1),
            show_edge=False,
        )
        tbl.add_column("ID", style="dim", width=5, no_wrap=True)
        tbl.add_column("Description", style="", min_width=36)
        tbl.add_column("Location", style="dim", width=22, no_wrap=True)

        for idx, item in enumerate(items, 1):
            row_id = f"{prefix}-{idx:02d}"
            desc = (
                item.get("title") or item.get("check") or item.get("description", "—")
            )[:72]
            location = item.get("location", "—")[:28]
            tbl.add_row(row_id, desc, location)

        console.print(tbl)

    console.print()
    if report_path.exists():
        console.print(f"  [green]Full report →[/green] {report_path}")
    else:
        console.print("  [yellow]audit_report.md not generated[/yellow]")
    console.print()


# ── Entry point ────────────────────────────────────────────────────────────────


def usage():
    console.print(
        Panel(
            "[bold]ignite[/bold] — EVM security audit agent (https://github.com/touchmeangel/ignite)\n\n"
            "  [green]ignite foundry .[/green]\n"
            "  ignite foundry /path/to/project\n"
            "  ignite foundry . [dim]--reconfigure[/dim]\n"
            "  ignite foundry . [dim]--update[/dim]\n\n"
            f"  Available managers: {', '.join(MANAGERS)}",
            title="Usage",
            box=box.ROUNDED,
        )
    )


def main():
    args = sys.argv[1:]
    flags = {a for a in args if a.startswith("--")}
    positional = [a for a in args if not a.startswith("--")]

    reconfigure = "--reconfigure" in flags
    do_update = "--update" in flags

    if len(positional) < 2:
        usage()
        sys.exit(1)

    manager = positional[0].lower()
    project_path = Path(positional[1]).resolve()

    if manager not in MANAGERS:
        console.print(
            f"  [red]✗[/red]  Unknown manager '{manager}'. "
            f"Available: {', '.join(MANAGERS)}"
        )
        sys.exit(1)

    if not project_path.exists():
        console.print(f"  [red]✗[/red]  Path not found: {project_path}")
        sys.exit(1)

    # ── Status & Setup ────────────────────────────────────────────────────────

    config_path = IGNITE_HOME / "config.json"
    console.print()

    if config_path.exists() and not reconfigure:
        cfg = json.loads(config_path.read_text())
        r_model = cfg.get("models", {}).get("reasoning", {}).get("model", "?")
        console.print(
            f"  [green]✔[/green]  Config found  [dim](reasoning: {r_model})[/dim]"
        )
    else:
        if reconfigure:
            console.print("  [dim]Reconfiguring …[/dim]")
        else:
            console.print("  [yellow]No config found.[/yellow]")

        mdef = MANAGERS[manager]
        if mdef["detect"](project_path):
            console.print(f"  [green]✔[/green]  Detected {mdef['label']} project")
        else:
            console.print(
                f"  [yellow]⚠[/yellow]  No {mdef['label']} config found in {project_path}"
            )

        if not docker_running():
            console.print(
                "  [red]✗[/red]  Docker is not running. Start Docker Desktop and retry."
            )
            sys.exit(1)
        console.print("  [green]✔[/green]  Docker available")

        run_setup(manager)

    # ── Docker Execution ──────────────────────────────────────────────────────

    if do_update:
        pull_image(manager)

    console.rule()
    console.print(f"\n  [bold]Running audit …[/bold]  [dim]{project_path}[/dim]\n")

    rc = run_container(manager, project_path, config_path)
    if rc != 0:
        console.print(f"  [red]✗[/red]  Container exited with code {rc}")

    print_report(project_path)


if __name__ == "__main__":
    main()
