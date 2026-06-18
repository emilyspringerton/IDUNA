"""
einhorn_sdk.colab — Colab-specific helpers for EINHORN_INDUSTRIAL observability.

Provides:
  - ColabEmily: an Emily Prime identity for Colab notebooks
  - setup_from_colab_secrets(): reads IDUNA_BASE_URL + credentials from Colab userdata
  - stream_apples(): SSE-style Apple tail using polling (Colab can't hold open TCP)
  - push_sprint(): one-liner to push a HEIMDAL requirement to Emily Prime
  - git_pull_rebase(): pull+rebase before pushing (distributed Emily discipline)

Colab usage:
    from einhorn_sdk.colab import ColabEmily

    emily = ColabEmily.from_colab_secrets()  # reads from Colab → Secrets panel
    emily.observe("Training started: epoch 1 of 5")
    emily.request("Analyze training curve and suggest hyperparameter changes")
"""

from __future__ import annotations

import os
import subprocess
import time
from typing import Optional

from .client import IdunaClient


class ColabEmily:
    """
    Emily Prime identity for a Colab notebook.

    Self-identifies as agent type 'emily_cluster' with a unique cluster ID
    so the production Emily Prime monolith can see it in /admin/agents.

    Authentication priority:
        1. Colab userdata secrets (via google.colab.userdata)
        2. Environment variables
        3. Explicit constructor args
    """

    CLUSTER_TYPE = "colab"

    def __init__(
        self,
        base_url: str,
        email: Optional[str] = None,
        password: Optional[str] = None,
        agent_name: Optional[str] = None,
        agent_secret: Optional[str] = None,
        cluster_id: str = "colab-1",
    ):
        self.cluster_id = cluster_id
        self.cluster_name = f"EMILY_PRIME_{cluster_id.upper().replace('-', '_')}"
        self._client = IdunaClient(base_url)

        if agent_name and agent_secret:
            self._client.auth_agent(agent_name, agent_secret)
        elif email and password:
            self._client.auth_local(email, password)
        else:
            raise ValueError("provide either (email, password) or (agent_name, agent_secret)")

    @classmethod
    def from_colab_secrets(cls, cluster_id: str = "colab-1") -> "ColabEmily":
        """
        Create a ColabEmily using Colab Secrets (the padlock panel).

        Expected secrets:
            IDUNA_BASE_URL    — e.g. http://your-server:8080
            IDUNA_EMAIL       — webmaster or operator email (for local auth)
            IDUNA_PASSWORD    — password (for local auth)
          OR:
            IDUNA_AGENT_NAME  — agent name (for M2M auth)
            IDUNA_AGENT_SECRET — agent secret (for M2M auth)
        """
        try:
            from google.colab import userdata  # type: ignore
            def get(key: str) -> Optional[str]:
                try:
                    return userdata.get(key)
                except Exception:
                    return None
        except ImportError:
            # Not in Colab — fall back to env vars.
            def get(key: str) -> Optional[str]:
                return os.getenv(key)

        base_url = get("IDUNA_BASE_URL") or os.getenv("IDUNA_BASE_URL", "http://localhost:8080")
        agent_name = get("IDUNA_AGENT_NAME") or os.getenv("IDUNA_AGENT_NAME")
        agent_secret = get("IDUNA_AGENT_SECRET") or os.getenv("IDUNA_AGENT_SECRET")
        email = get("IDUNA_EMAIL") or os.getenv("IDUNA_EMAIL")
        password = get("IDUNA_PASSWORD") or os.getenv("IDUNA_PASSWORD")

        if agent_name and agent_secret:
            return cls(base_url, agent_name=agent_name, agent_secret=agent_secret, cluster_id=cluster_id)
        elif email and password:
            return cls(base_url, email=email, password=password, cluster_id=cluster_id)
        else:
            raise ValueError(
                "Set IDUNA_EMAIL+IDUNA_PASSWORD or IDUNA_AGENT_NAME+IDUNA_AGENT_SECRET "
                "in Colab Secrets (padlock panel) or environment variables."
            )

    @property
    def client(self) -> IdunaClient:
        return self._client

    # ── observability hooks ───────────────────────────────────────────────────

    def observe(self, observation: str, source_repo: str = "colab") -> dict:
        """Post an observation Apple from this Colab session."""
        title = observation[:120]
        body = f"[{self.cluster_name}] {observation}"
        return self._client.apples.post(
            "observation", title, body=body, source_repo=source_repo,
            metadata={"cluster_id": self.cluster_id, "cluster_type": self.CLUSTER_TYPE},
        )

    def complete(self, title: str, body: str = "", source_repo: str = "colab") -> dict:
        """Post a completion Apple from this Colab session."""
        return self._client.apples.post(
            "completion", title, body=body, source_repo=source_repo,
            metadata={"cluster_id": self.cluster_id, "cluster_type": self.CLUSTER_TYPE},
        )

    def escalate(self, message: str, source_repo: str = "colab") -> dict:
        """Post an escalation Apple — surfaces in Emily Prime as high-priority."""
        return self._client.apples.post(
            "escalation", message[:120], body=message, source_repo=source_repo,
            metadata={"cluster_id": self.cluster_id, "cluster_type": self.CLUSTER_TYPE},
        )

    def request(self, requirement: str) -> dict:
        """
        Submit a requirement to Emily Prime via HEIMDAL.
        Emily Prime will pick it up on the next RSI cycle and execute it.

        Returns the SprintItem (status=pending).
        """
        return self._client.heimdal.create_sprint(requirement, agent_name=self.cluster_name)

    # ── Apple stream ──────────────────────────────────────────────────────────

    def tail_apples(
        self,
        n: int = 10,
        poll_interval: float = 5.0,
        duration_seconds: float = 60.0,
        apple_type: str = "",
    ):
        """
        Poll the Apple stream for new entries and print them.
        Runs for duration_seconds (Colab-safe: no persistent TCP connection).

        Usage:
            emily.tail_apples(n=5, duration_seconds=120)
        """
        seen_ids: set = set()
        end = time.time() + duration_seconds
        print(f"[einhorn_sdk] Tailing Apples for {duration_seconds}s (Ctrl+C to stop)...")
        while time.time() < end:
            try:
                apples = self._client.apples.list(limit=n, apple_type=apple_type)
                for apple in reversed(apples):
                    aid = apple.get("id")
                    if aid not in seen_ids:
                        seen_ids.add(aid)
                        print(f"  Apple #{aid} [{apple.get('type')}] {apple.get('title')}")
            except Exception as e:
                print(f"  [warn] tail error: {e}")
            time.sleep(poll_interval)

    # ── drive upload shorthand ────────────────────────────────────────────────

    def upload_file(self, local_path: str, mime_type: str = "application/octet-stream") -> dict:
        """Upload a local file to Google Drive via IDUNA."""
        import os
        filename = os.path.basename(local_path)
        with open(local_path, "rb") as f:
            data = f.read()
        result = self._client.drive.upload(filename, data, mime_type=mime_type)
        print(f"[einhorn_sdk] Uploaded {filename} → {result.get('web_view_link') or result.get('id')}")
        return result


# ── git helpers ───────────────────────────────────────────────────────────────

def git_pull_rebase(repo_path: str = ".") -> bool:
    """
    Pull and rebase from origin/main before pushing.
    Part of the distributed Emily discipline (S44-05).

    Returns True on success, False on conflict (emits escalation Apple if emily provided).
    """
    try:
        result = subprocess.run(
            ["git", "-C", repo_path, "pull", "--rebase", "origin", "main"],
            capture_output=True, text=True, timeout=60,
        )
        if result.returncode == 0:
            print(f"[git] pull --rebase OK: {result.stdout.strip() or 'up to date'}")
            return True
        else:
            print(f"[git] pull --rebase FAILED:\n{result.stderr.strip()}")
            return False
    except subprocess.TimeoutExpired:
        print("[git] pull --rebase timed out")
        return False
    except Exception as e:
        print(f"[git] pull --rebase error: {e}")
        return False
