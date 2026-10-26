"""Check resolved profile overlays with synthetic inputs; start no containers."""

import json
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="kycalendar-profile-check-") as directory:
    fixture = Path(directory) / ".env"
    fixture.write_text(
        "KY_APP_URL=https://calendar.example.invalid\n"
        "KY_SESSION_SECRET=synthetic-calendar-profile-secret\n"
        "KYCALENDAR_DNS=192.0.2.53\n"
        "KY_CONTAINER_IP=192.0.2.10\n"
        "KY_NETWORK_SUBNET=192.0.2.0/24\n"
    )
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("COMPOSE_", "KY_", "KYCALENDAR_"))}

    def render(files):
        command = ["docker", "compose", "--project-name", "kycalendar-profile-check",
                   "--env-file", str(fixture)]
        for file in files:
            command += ["-f", file]
        return json.loads(subprocess.check_output(command + ["config", "--format", "json"],
                                                 cwd=root, env=env))

    for overlays in [[], ["docker-compose.build.yml"], ["docker-compose.lan-dns.yml"],
                     ["docker-compose.build.yml", "docker-compose.lan-dns.yml",
                      "docker-compose.static-ip.yml"]]:
        files = ["docker-compose.yml", *overlays]
        baseline = render(files)
        configured = render([*files, "docker-compose.strong-crypto.yml"])
        assert configured["services"]["app"]["environment"].pop("GODEBUG") == "fips140=on"
        assert configured == baseline, "The profile changed another Compose setting"
print("All four chains retain data, credentials, image/build, network and DNS settings.")
