import json
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path
from urllib.request import urlopen

import questionary
from questionary import Style
from rich import box
from rich.console import Console
from rich.live import Live
from rich.panel import Panel
from rich.spinner import Spinner
from rich.text import Text

IGNITE_HOME = Path.home() / ".ignite"

console = Console()

Q_STYLE = Style(
    [
        ("qmark", "fg:cyan bold"),
        ("question", "bold"),
        ("answer", "fg:cyan bold"),
        ("pointer", "fg:cyan bold"),
        ("highlighted", "fg:cyan bold"),
        ("selected", "fg:cyan"),
        ("separator", "fg:#665c54"),
        ("instruction", "fg:#7c6f64"),
    ]
)

MANAGERS: dict[str, dict] = {
    "foundry": {
        "label": "Foundry",
        "detect": lambda p: (
            (p / "foundry.toml").exists() or (p / "forge.toml").exists()
        ),
        "image": "touchmeangel/ignite_agent:latest",
    }
}

PROVIDERS = {
    "anthropic": {
        "label": "Anthropic",
        "models": [
            "claude-opus-4-8",
            "claude-opus-4-7",
            "claude-sonnet-4-6",
            "claude-opus-4-6",
            "claude-haiku-4.5",
        ],
        "env_key": "ANTHROPIC_API_KEY",
        "provider_str": "anthropic",
        "base_url": None,
    },
    "openai": {
        "label": "OpenAI",
        "models": [
            "gpt-5.5",
            "gpt-5",
            "gpt-4.1-mini",
            "gpt-4o",
        ],
        "env_key": "OPENAI_API_KEY",
        "provider_str": "openai",
        "base_url": None,
    },
    "google": {
        "label": "Google",
        "models": [
            "gemini-3.5-flash",
            "gemini-3.1-pro",
            "gemini-3.1-flash-lite",
            "gemini-2.5-flash",
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
    "custom_openai_compatible": {
        "label": "Custom (OpenAI Compatible Gateway)",
        "models": [],
        "env_key": "OPENAI_API_KEY",
        "provider_str": "openai_compatible",
        "base_url": "http://localhost:8000/v1",
    },
}

FALLBACK_OLLAMA_MODELS = [
    "qwen2.5-coder:14b",
    "qwen2.5-coder:7b",
    "deepseek-coder:33b",
    "deepseek-coder:6.7b",
    "llama3.1:8b",
]

CUSTOM_MODEL_LABEL = "[ enter custom model id ]"


def handle_abort():
    """Helper to print a clean exit message when the user interrupts the execution."""
    console.print(
        "\n\n  [dim]⚠[/dim]  [bold]Execution cancelled by user. Exiting...[/bold]\n"
    )
    sys.exit(130)  # 130 is the standard exit code for SIGINT (Ctrl+C)


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


def _ask_model_profile(role_name: str) -> tuple[str, str, str | None]:
    provider_label = questionary.select(
        f"Select provider for [{role_name.upper()}] engine:",
        choices=[p["label"] for p in PROVIDERS.values()],
        style=Q_STYLE,
    ).ask()
    if provider_label is None:
        handle_abort()

    prov_key = next(k for k, v in PROVIDERS.items() if v["label"] == provider_label)
    prov = PROVIDERS[prov_key]

    if prov_key == "ollama":
        base_url = questionary.text(
            "Ollama endpoint address:", default=prov["base_url"], style=Q_STYLE
        ).ask()
        if base_url is None:
            handle_abort()
        base_url = base_url or prov["base_url"]

        container_url = base_url
        if "localhost" in base_url or "127.0.0.1" in base_url:
            container_url = "http://host.docker.internal:11434"

        models = get_ollama_models(base_url)
        choices = (models or FALLBACK_OLLAMA_MODELS) + [CUSTOM_MODEL_LABEL]

        model = questionary.select(
            "Select model tag:", choices=choices, style=Q_STYLE
        ).ask()
        if model is None:
            handle_abort()

        if model == CUSTOM_MODEL_LABEL:
            model = questionary.text(
                "Model tag (e.g. llama3.1:8b):", style=Q_STYLE
            ).ask()
            if model is None:
                handle_abort()
            model = model or FALLBACK_OLLAMA_MODELS[0]
        return prov["provider_str"], model, container_url

    if prov_key == "custom_openai_compatible":
        base_url = questionary.text(
            "Target API Base URL endpoint:", default=prov["base_url"], style=Q_STYLE
        ).ask()
        if base_url is None:
            handle_abort()
        base_url = base_url or prov["base_url"]

        model = questionary.text(
            "Target model id (e.g. openrouter/auto, deepseek-chat):", style=Q_STYLE
        ).ask()
        if model is None:
            handle_abort()
        model = model or "gpt-4o"

        return prov["provider_str"], model, base_url

    model = questionary.select(
        "Select a model variant:",
        choices=prov["models"] + [CUSTOM_MODEL_LABEL],
        style=Q_STYLE,
    ).ask()
    if model is None:
        handle_abort()

    if model == CUSTOM_MODEL_LABEL:
        model = questionary.text("Model signature string:", style=Q_STYLE).ask()
        if not model:
            handle_abort()

    return prov["provider_str"], model, prov.get("base_url")


def _ask_api_key(provider_str: str) -> str:
    prov = next(
        (p for p in PROVIDERS.values() if p["provider_str"] == provider_str), {}
    )
    env_key = prov.get("env_key")
    if not env_key:
        return ""

    key = questionary.password(f"{env_key}:", style=Q_STYLE).ask()
    if key is None:
        handle_abort()

    if not key:
        console.print(
            f"  [dim]⚠  Value omitted — initialize {env_key} inside environment values later[/dim]"
        )
    return key


CONFIRMED_PATHS_FILE = IGNITE_HOME / "confirmed_paths.json"


def _load_confirmed_paths() -> set[str]:
    if CONFIRMED_PATHS_FILE.exists():
        try:
            return set(json.loads(CONFIRMED_PATHS_FILE.read_text()))
        except Exception:
            return set()
    return set()


def _save_confirmed_paths(paths: set[str]) -> None:
    IGNITE_HOME.mkdir(parents=True, exist_ok=True)
    CONFIRMED_PATHS_FILE.write_text(json.dumps(sorted(paths), indent=2))


def confirm_folder_access(project_path: str, auto_confirm: bool = False) -> None:
    confirmed = _load_confirmed_paths()
    if project_path in confirmed:
        return

    warning_text = (
        f"[bold red]AGENT FILE SYSTEM ACCESS[/bold red]\n\n"
        f"You are about to run an autonomous agent on: [cyan]{project_path}[/cyan]\n"
        f"The agent will have full permissions to [bold]read, write, and execute tools[/bold] "
        f"inside this directory.\n\n"
        f"[dim]Ensure you have committed any sensitive local changes to git before proceeding.[/dim]"
    )
    console.print(Panel(warning_text, border_style="red", padding=(1, 2)))

    if auto_confirm:
        console.print(
            f"  [yellow]⚠[/yellow]  [bold]-y flag set[/bold] — auto-accepting risk for [cyan]{project_path}[/cyan]\n"
        )
        confirmed.add(project_path)
        _save_confirmed_paths(confirmed)
        return

    granted = questionary.confirm(
        f"Grant the agent write permissions to {project_path}?",
        default=False,
        style=Q_STYLE,
    ).ask()

    if not granted:
        console.print("\n  [dim]⚠  Execution aborted by user. Safe choice![/dim]\n")
        sys.exit(0)

    confirmed.add(project_path)
    _save_confirmed_paths(confirmed)


def run_setup(manager: str) -> dict:
    config_path = IGNITE_HOME / "config.json"
    env_path = IGNITE_HOME / ".env"
    IGNITE_HOME.mkdir(parents=True, exist_ok=True)

    console.print()
    console.rule(
        "[dim]STEP 1 — Flash Model (Fast routing tasks, sub-scans)[/dim]",
        style="dim",
    )
    f_provider, f_model, f_base_url = _ask_model_profile("flash")

    console.print()
    console.rule(
        "[dim]STEP 2 — Reasoning Model (Deep analysis, logic reviews)[/dim]",
        style="dim",
    )
    r_provider, r_model, r_base_url = _ask_model_profile("reasoning")

    console.print()
    console.rule(
        "[dim]STEP 3 — Security Credentials & Token Storage[/dim]", style="dim"
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
    console.print(f"\n  [cyan]✔[/cyan]  config.json  →  {config_path}", highlight=False)

    env_lines = ["# Generated by ignite — do not commit", ""]
    for provider_id, token_value in collected_keys.items():
        matched_prov = next(
            (p for p in PROVIDERS.values() if p["provider_str"] == provider_id), {}
        )
        if matched_prov.get("env_key"):
            env_lines.append(f"{matched_prov['env_key']}={token_value}")

    env_path.write_text("\n".join(env_lines) + "\n")
    console.print(f"  [cyan]✔[/cyan]  .env         →  {env_path}", highlight=False)

    return config


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
    console.print(f"  [dim]Pulling latest image: {image} …[/dim]", highlight=False)
    subprocess.run([docker_path, "pull", image])  # noqa: S603, S607
    console.print("  [cyan]✔[/cyan]  Image updated.")


def _ensure_debug_file(debug_path: Path) -> None:
    if debug_path.is_dir():
        try:
            debug_path.rmdir()
        except OSError:
            console.print(
                f"  [red]✗[/red]  {debug_path} is a non-empty directory — "
                f"remove it manually and retry:\n      rm -r {debug_path}"
            )
            sys.exit(1)
    debug_path.parent.mkdir(parents=True, exist_ok=True)
    debug_path.touch(exist_ok=True)


def run_container(
    manager: str, project_path: Path, config_path: Path, debug_path: Path
) -> int:
    _ensure_debug_file(debug_path)
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

    cmd = [
        docker_path,
        "run",
        "--rm",
        "-t",
        "-v",
        f"{project_path}:/project",
        "-v",
        f"{config_path}:/app/config.json:ro",
        "-v",
        f"{debug_path}:/app/debug.log:rw",
        *user_mapping,
        "-e",
        "PROJECT_PATH=/project",
        *env_flags,
        *extra_hosts,
        image,
        "--project-path",
        "/project",
        "--output",
        "/project/agent_results.json",
        "--debug",
        "/app/debug.log",
    ]

    console.print(f"  [dim]$ {' '.join(cmd)}[/dim]\n", highlight=False)

    process = subprocess.Popen(  # noqa: S603
        cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, bufsize=0
    )

    try:
        custom_spinner = Spinner("dots", text="Initializing container...", style="cyan")
        custom_spinner.frames = ["·", "*", "✷", "✸", "✹", "✺", "✹", "✸", "✷", "*"]
        custom_spinner.interval = 120

        with Live(
            custom_spinner, console=console, refresh_per_second=20, transient=True
        ) as live:
            is_first_line = True
            while True:
                chunk = process.stdout.read(1)

                if not chunk and process.poll() is not None:
                    break

                if chunk:
                    if is_first_line:
                        live.stop()
                        is_first_line = False

                    sys.stdout.buffer.write(chunk)
                    sys.stdout.buffer.flush()

    except KeyboardInterrupt:
        process.terminate()
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            process.kill()
        raise

    return process.returncode


def usage():
    console.print(
        Panel(
            "[bold]ignite[/bold] — EVM security research agent (https://github.com/touchmeangel/ignite_agent)\n\n"
            "  [cyan]ignite[/cyan]                             [dim](Auto-detect manager in current directory)[/dim]\n"
            "  [cyan]ignite -u[/cyan]                          [dim](Pull latest image and run in current directory)[/dim]\n"
            "  ignite foundry .                   [dim](Explicit manager and path)[/dim]\n"
            "  ignite /path/to/project            [dim](Explicit path, auto-detect manager)[/dim]\n"
            "  ignite /path/to/project -y         [dim](Allow access to new folder without manual input)[/dim]\n"
            "  ignite . [dim]-r or --reconfigure[/dim]\n"
            "  ignite [dim]-h or --help[/dim]                      [dim](Show this help utility view)[/dim]\n\n"
            f"  Available managers: {', '.join(MANAGERS)}",
            title="Usage",
            box=box.ROUNDED,
        )
    )


def main():
    try:
        args = sys.argv[1:]
        flags = {a for a in args if a.startswith("-")}
        positional = [a for a in args if not a.startswith("-")]

        if "--help" in flags or "-h" in flags:
            usage()
            sys.exit(0)

        reconfigure = "--reconfigure" in flags or "-r" in flags
        do_update = "--update" in flags or "-u" in flags
        allow_folder_access = "-y" in flags

        manager = None
        project_path = Path(".").resolve()

        if len(positional) == 2:
            manager = positional[0].lower()
            project_path = Path(positional[1]).resolve()
        elif len(positional) == 1:
            val = positional[0]
            if val.lower() in MANAGERS:
                manager = val.lower()
            else:
                project_path = Path(val).resolve()
        elif len(positional) > 2:
            usage()
            sys.exit(1)

        if not project_path.exists():
            console.print(
                f"  [red]✗[/red]  Path not found: {project_path}", highlight=False
            )
            sys.exit(1)

        if manager is None:
            detected = [
                m_name
                for m_name, m_def in MANAGERS.items()
                if m_def["detect"](project_path)
            ]

            if len(detected) == 1:
                manager = detected[0]
            elif len(detected) > 1:
                console.print(
                    f"  [red]✗[/red]  Conflict: Multiple managers detected ({', '.join(detected)})."
                )
                console.print(
                    "      Please specify your target manager explicitly (e.g., ignite foundry .)"
                )
                sys.exit(1)
            else:
                console.print(
                    f"  [red]✗[/red]  Could not auto-detect an EVM project setup at [dim]{project_path}[/dim]"
                )
                console.print(f"      Available options: {', '.join(MANAGERS)}")
                sys.exit(1)

        if manager not in MANAGERS:
            console.print(
                f"  [red]✗[/red]  Unknown manager '{manager}'. "
                f"Available: {', '.join(MANAGERS)}"
            )
            sys.exit(1)

        console.print(f"  [dim]➔ Detected manager:[/dim] [cyan]{manager}[/cyan]")

        config_path = IGNITE_HOME / "config.json"
        console.print()

        if config_path.exists() and not reconfigure:
            cfg = json.loads(config_path.read_text())
            r_model = cfg.get("models", {}).get("reasoning", {}).get("model", "?")
            console.print(
                f"  [cyan]✔[/cyan]  Config found  [dim](reasoning: {r_model})[/dim]"
            )
        else:
            if reconfigure:
                console.print("  [dim]Reconfiguring …[/dim]")
            else:
                console.print("  [dim]No config found.[/dim]")

            mdef = MANAGERS[manager]
            console.print(f"  [cyan]✔[/cyan]  Using {mdef['label']} framework pipeline")

            if not docker_running():
                console.print(
                    "  [red]✗[/red]  Docker is not running. Start Docker Desktop and retry."
                )
                sys.exit(1)
            console.print("  [cyan]✔[/cyan]  Docker available")

            run_setup(manager)

        if do_update:
            pull_image(manager)

        confirm_folder_access(str(project_path), auto_confirm=allow_folder_access)

        console.rule(style="dim")

        console.print(
            f"\n  Running agent  [dim]{project_path}[/dim]\n", highlight=False
        )

        debug_path = IGNITE_HOME / "debug.log"
        rc = run_container(manager, project_path, config_path, debug_path)
        if rc != 0:
            console.print(
                f"\n  [red]✗[/red] [bold red]Container execution failed (exit code {rc}).[/bold red]\n"
            )
            sys.exit(rc)

        results_path = project_path / "agent_results.json"

        completion_text = Text.assemble(
            ("AUDIT ENGINE PIPELINE COMPLETE\n\n", "bold cyan"),
            ("Status:     ", "dim"),
            ("Active / Success\n", "bold"),
            ("Artifacts:  ", "dim"),
            (f"{results_path.name}\n", "cyan"),
            ("Location:   ", "dim"),
            (f"{project_path}", "italic dim"),
        )

        console.print(
            Panel(
                completion_text,
                box=box.ROUNDED,
                border_style="cyan",
                padding=(1, 2),
                expand=False,
            ),
            highlight=False,
        )
        console.print()

    except KeyboardInterrupt:
        handle_abort()


if __name__ == "__main__":
    main()
