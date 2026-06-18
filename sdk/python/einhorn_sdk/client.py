"""
IdunaClient — the main entry point for the einhorn_sdk.

Handles authentication (local password or M2M agent), token refresh,
and exposes sub-clients for each API domain.
"""

from __future__ import annotations

import time
from typing import Optional

import requests


class IdunaClient:
    """
    Authenticated client for the IDUNA IAM API.

    Usage — local password auth (webmaster or operator):
        client = IdunaClient("http://localhost:8080")
        client.auth_local("webmaster@localhost", "password")

    Usage — M2M agent auth (for Emily Prime or other agents):
        client = IdunaClient("http://localhost:8080")
        client.auth_agent("EMILY_PRIME", "secret")

    Usage — pass token directly (Colab: token stored in env or secrets):
        client = IdunaClient("http://localhost:8080", token="eyJ...")

    After auth, sub-clients are available:
        client.users     — user CRUD
        client.apples    — Apple audit trail
        client.drive     — Google Drive upload/list
        client.heimdal   — sprint planning
        client.agents    — agent management
        client.health    — health check
    """

    def __init__(self, base_url: str, token: Optional[str] = None, timeout: int = 30):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self._token: Optional[str] = token
        self._token_expires_at: int = 0
        self._session = requests.Session()

        # Sub-clients
        self.users = UsersClient(self)
        self.apples = ApplesClient(self)
        self.drive = DriveClient(self)
        self.heimdal = HeimdalClient(self)
        self.agents = AgentsClient(self)
        self.health = HealthClient(self)

    # ── auth ──────────────────────────────────────────────────────────────────

    def auth_local(self, email: str, password: str) -> dict:
        """Authenticate with email+password. Webmaster (uid=0) has full admin access."""
        resp = self._post_public("/api/v1/auth/local", {"email": email, "password": password})
        self._token = resp["token"]
        self._token_expires_at = resp.get("expires_at", 0)
        return resp

    def auth_agent(self, agent_name: str, agent_secret: str) -> dict:
        """Authenticate as an M2M agent. Returns JWT with agent permissions."""
        resp = self._post_public("/api/v1/auth/agent", {"agent_name": agent_name, "agent_secret": agent_secret})
        self._token = resp["token"]
        self._token_expires_at = resp.get("expires_at", 0)
        return resp

    def set_token(self, token: str, expires_at: int = 0):
        """Set a pre-existing token directly (e.g., from a Colab secret)."""
        self._token = token
        self._token_expires_at = expires_at

    def is_authenticated(self) -> bool:
        if not self._token:
            return False
        if self._token_expires_at > 0 and time.time() > self._token_expires_at - 60:
            return False
        return True

    # ── low-level HTTP ────────────────────────────────────────────────────────

    def _headers(self) -> dict:
        h = {"Content-Type": "application/json"}
        if self._token:
            h["Authorization"] = f"Bearer {self._token}"
        return h

    def _get(self, path: str, params: Optional[dict] = None) -> dict | list:
        r = self._session.get(
            self.base_url + path,
            headers=self._headers(),
            params=params,
            timeout=self.timeout,
        )
        _raise_for_status(r)
        return r.json()

    def _post(self, path: str, body: dict) -> dict:
        r = self._session.post(
            self.base_url + path,
            json=body,
            headers=self._headers(),
            timeout=self.timeout,
        )
        _raise_for_status(r)
        return r.json()

    def _patch(self, path: str, body: dict) -> dict:
        r = self._session.patch(
            self.base_url + path,
            json=body,
            headers=self._headers(),
            timeout=self.timeout,
        )
        _raise_for_status(r)
        return r.json()

    def _delete(self, path: str) -> None:
        r = self._session.delete(
            self.base_url + path,
            headers=self._headers(),
            timeout=self.timeout,
        )
        _raise_for_status(r)

    def _post_public(self, path: str, body: dict) -> dict:
        r = self._session.post(
            self.base_url + path,
            json=body,
            timeout=self.timeout,
        )
        _raise_for_status(r)
        return r.json()

    def openapi_spec(self) -> dict:
        """Fetch the live OpenAPI spec from IDUNA."""
        r = self._session.get(self.base_url + "/api/v1/openapi.json", timeout=self.timeout)
        _raise_for_status(r)
        return r.json()


# ── sub-clients ───────────────────────────────────────────────────────────────

class UsersClient:
    """User CRUD. Requires users.admin permission (webmaster token)."""

    def __init__(self, parent: IdunaClient):
        self._c = parent

    def create(self, email: str, password: str, display_name: str = "") -> dict:
        """Create a new local user. Returns the created user."""
        return self._c._post("/api/v1/users", {
            "email": email,
            "password": password,
            "display_name": display_name,
        })

    def get(self, uid: int) -> dict:
        """Get a user by local_uid."""
        return self._c._get(f"/api/v1/users/{uid}")

    def list(self, limit: int = 100) -> list:
        """List all non-deleted local users."""
        return self._c._get("/api/v1/users", params={"limit": limit})

    def update(self, uid: int, **kwargs) -> dict:
        """
        Update a user. Accepted kwargs: email, display_name, password, status.
        status must be 'active' or 'suspended'.
        """
        return self._c._patch(f"/api/v1/users/{uid}", kwargs)

    def delete(self, uid: int) -> None:
        """Soft-delete a user (sets status=deleted). Cannot delete uid=0."""
        self._c._delete(f"/api/v1/users/{uid}")

    def change_password(self, uid: int, new_password: str) -> dict:
        """Convenience: change a user's password."""
        return self.update(uid, password=new_password)

    def suspend(self, uid: int) -> dict:
        """Suspend a user account."""
        return self.update(uid, status="suspended")

    def activate(self, uid: int) -> dict:
        """Reactivate a suspended user."""
        return self.update(uid, status="active")


class ApplesClient:
    """Apple audit trail. Apples are golden documentation records."""

    def __init__(self, parent: IdunaClient):
        self._c = parent

    def post(
        self,
        apple_type: str,
        title: str,
        body: str = "",
        source_repo: str = "",
        metadata: Optional[dict] = None,
    ) -> dict:
        """
        File an Apple to the IDUNA audit trail.

        apple_type: improvement | observation | audit | escalation | completion | backlog_completion
        """
        payload = {"type": apple_type, "title": title}
        if body:
            payload["body"] = body
        if source_repo:
            payload["source_repo"] = source_repo
        if metadata:
            payload["metadata"] = metadata
        return self._c._post("/api/v1/apples", payload)

    def list(
        self,
        limit: int = 50,
        apple_type: str = "",
        source_repo: str = "",
    ) -> list:
        params = {"limit": limit}
        if apple_type:
            params["apple_type"] = apple_type
        if source_repo:
            params["source_repo"] = source_repo
        return self._c._get("/api/v1/apples", params=params)

    def get(self, apple_id: int) -> dict:
        return self._c._get(f"/api/v1/apples/{apple_id}")

    def observe(self, observation: str, source_repo: str = "colab") -> dict:
        """Shorthand: post an observation Apple from Colab."""
        return self.post("observation", observation[:120], body=observation, source_repo=source_repo)

    def completion(self, title: str, body: str = "", source_repo: str = "colab") -> dict:
        """Shorthand: post a completion Apple from Colab."""
        return self.post("completion", title, body=body, source_repo=source_repo)


class DriveClient:
    """Google Drive integration via IDUNA. Requires drive.write / drive.read permissions."""

    def __init__(self, parent: IdunaClient):
        self._c = parent

    def upload(self, filename: str, data: bytes, mime_type: str = "application/octet-stream") -> dict:
        """
        Upload a file to the configured Google Drive folder.
        Returns DriveFile metadata (id, name, web_view_link, size, ...).

        Max upload size: 32 MB (IDUNA server limit).
        """
        r = self._c._session.post(
            self._c.base_url + "/api/v1/drive/upload",
            files={"file": (filename, data, mime_type)},
            data={"filename": filename, "mime_type": mime_type},
            headers={"Authorization": f"Bearer {self._c._token}"},
            timeout=120,
        )
        _raise_for_status(r)
        return r.json()

    def list(self) -> list:
        """List files in the configured Drive folder."""
        return self._c._get("/api/v1/drive/files")

    def get(self, file_id: str) -> dict:
        """Get metadata for a single Drive file by ID."""
        return self._c._get(f"/api/v1/drive/files/{file_id}")


class HeimdalClient:
    """HEIMDAL sprint planning — submit requirements to Emily Prime."""

    def __init__(self, parent: IdunaClient):
        self._c = parent

    def create_sprint(self, requirement: str, agent_name: str = "colab") -> dict:
        """
        Submit a product requirement to Emily Prime via HEIMDAL.
        Emily Prime will translate it to an RSI roadmap item and execute it.

        Returns the created SprintItem (status=pending).
        """
        return self._c._post("/api/v1/heimdal/sprints", {
            "requirement": requirement,
            "agent_name": agent_name,
        })

    def list(
        self,
        agent_name: str = "",
        status: str = "",
        limit: int = 20,
    ) -> list:
        params = {"limit": limit}
        if agent_name:
            params["agent_name"] = agent_name
        if status:
            params["status"] = status
        return self._c._get("/api/v1/heimdal/sprints", params=params)

    def get(self, sprint_id: int) -> dict:
        return self._c._get(f"/api/v1/heimdal/sprints/{sprint_id}")

    def update(self, sprint_id: int, **kwargs) -> dict:
        """Update a sprint (status, roadmap_id, apple_id, criteria)."""
        return self._c._patch(f"/api/v1/heimdal/sprints/{sprint_id}", kwargs)

    def request(self, requirement: str, agent_name: str = "colab") -> dict:
        """Alias for create_sprint — more natural in Colab context."""
        return self.create_sprint(requirement, agent_name=agent_name)


class AgentsClient:
    """Agent management — list and inspect M2M agents."""

    def __init__(self, parent: IdunaClient):
        self._c = parent

    def list(self) -> list:
        return self._c._get("/api/v1/agents")

    def get(self, agent_id: str) -> dict:
        return self._c._get(f"/api/v1/agents/{agent_id}")


class HealthClient:
    """Health check."""

    def __init__(self, parent: IdunaClient):
        self._c = parent

    def check(self) -> dict:
        r = self._c._session.get(self._c.base_url + "/health", timeout=10)
        return {"status": "ok" if r.status_code == 200 else "error", "code": r.status_code}


# ── error handling ────────────────────────────────────────────────────────────

class IdunaError(Exception):
    """Raised when the IDUNA API returns a non-2xx response."""

    def __init__(self, status_code: int, message: str):
        self.status_code = status_code
        self.message = message
        super().__init__(f"IDUNA {status_code}: {message}")


def _raise_for_status(response: requests.Response) -> None:
    if response.status_code >= 400:
        try:
            body = response.json()
            msg = body.get("error") or body.get("message") or response.text[:200]
        except Exception:
            msg = response.text[:200]
        raise IdunaError(response.status_code, msg)


# ── Optional type alias for type checkers ──────────────────────────────────

Optional = __import__("typing").Optional
