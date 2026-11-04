"""Hosts the Haat web game on Modal in one always-on container.

The container runs the same Go binary as `make serve`: the web game, the day
clock, the shopkeeper agents and the villagers, against NEAR testnet. Keys
come from the gitignored .env file and are stored by Modal as a secret.

Deploy with `make deploy` (builds bin/haat-linux-amd64 first).
"""

import pathlib
import subprocess

import modal

ROOT = pathlib.Path(__file__).resolve().parent.parent
BINARY = ROOT / "bin" / "haat-linux-amd64"

image = (
    modal.Image.debian_slim(python_version="3.12")
    .apt_install("ca-certificates")
    .add_local_file(BINARY, "/app/haat", copy=True)
    .run_commands("chmod +x /app/haat")
)

app = modal.App("haat")


def read_env(path: pathlib.Path) -> dict:
    """Reads KEY=VALUE lines from the gitignored settings file."""
    values = {}
    if path.exists():
        for line in path.read_text().splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                key, value = line.split("=", 1)
                values[key.strip()] = value.strip().strip("\"'")
    return values


hosted = modal.Secret.from_dict(
    {
        "PORT": "8080",
        "NEAR_NETWORK": "testnet",
        "HAAT_TRUST_PROXY": "1",
        "HAAT_DAY_MINUTES": "15",
        "HAAT_IDLE_MINUTES": "30",
        "HAAT_FF_PER_HOUR": "2",
        "HAAT_DAILY_TX_BUDGET": "1500",
        "HAAT_ACCOUNT_POOL": "2",
    }
)


@app.function(
    image=image,
    secrets=[modal.Secret.from_dict(read_env(ROOT / ".env")), hosted],
    min_containers=1,
    max_containers=1,
    timeout=24 * 60 * 60,
)
@modal.concurrent(max_inputs=200)
@modal.web_server(8080, startup_timeout=120)
def web():
    # Settings come from the environment; no settings file inside the container.
    subprocess.Popen(["/app/haat", "-env", "/tmp/haat.env", "serve"])
