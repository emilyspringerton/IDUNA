"""Tests for einhorn_sdk using responses to mock HTTP."""
import json
import pytest
import responses as resp_lib

from einhorn_sdk import IdunaClient
from einhorn_sdk.client import IdunaError


BASE = "http://localhost:8080"


@resp_lib.activate
def test_auth_local_sets_token():
    resp_lib.add(
        resp_lib.POST,
        f"{BASE}/api/v1/auth/local",
        json={"token": "eyJ.test.token", "expires_at": 9999999999, "sub": "local:0", "uid": 0},
        status=200,
    )
    client = IdunaClient(BASE)
    result = client.auth_local("webmaster@localhost", "secret")
    assert result["token"] == "eyJ.test.token"
    assert client._token == "eyJ.test.token"
    assert client.is_authenticated()


@resp_lib.activate
def test_auth_agent_sets_token():
    resp_lib.add(
        resp_lib.POST,
        f"{BASE}/api/v1/auth/agent",
        json={"token": "agent.jwt.here", "expires_at": 9999999999, "sub": "agent:EMILY_PRIME"},
        status=200,
    )
    client = IdunaClient(BASE)
    result = client.auth_agent("EMILY_PRIME", "supersecret")
    assert client._token == "agent.jwt.here"


@resp_lib.activate
def test_apples_post():
    resp_lib.add(
        resp_lib.POST,
        f"{BASE}/api/v1/auth/local",
        json={"token": "tok", "expires_at": 9999999999},
        status=200,
    )
    resp_lib.add(
        resp_lib.POST,
        f"{BASE}/api/v1/apples",
        json={"id": 42, "type": "observation", "title": "test apple"},
        status=201,
    )
    client = IdunaClient(BASE)
    client.auth_local("webmaster@localhost", "pw")
    apple = client.apples.post("observation", "test apple")
    assert apple["id"] == 42


@resp_lib.activate
def test_users_create_and_list():
    resp_lib.add(resp_lib.POST, f"{BASE}/api/v1/auth/local",
                 json={"token": "tok", "expires_at": 9999999999}, status=200)
    resp_lib.add(resp_lib.POST, f"{BASE}/api/v1/users",
                 json={"local_uid": 1, "email": "new@user.com", "status": "active"}, status=201)
    resp_lib.add(resp_lib.GET, f"{BASE}/api/v1/users",
                 json=[{"local_uid": 0, "email": "wm@local"}, {"local_uid": 1, "email": "new@user.com"}],
                 status=200)

    client = IdunaClient(BASE)
    client.auth_local("webmaster@localhost", "pw")

    user = client.users.create("new@user.com", "pass123", display_name="New User")
    assert user["local_uid"] == 1

    users = client.users.list()
    assert len(users) == 2


@resp_lib.activate
def test_error_raises_iduna_error():
    resp_lib.add(resp_lib.GET, f"{BASE}/api/v1/users",
                 json={"error": "forbidden"}, status=403)

    client = IdunaClient(BASE, token="tok")
    with pytest.raises(IdunaError) as exc_info:
        client.users.list()
    assert exc_info.value.status_code == 403
    assert "forbidden" in exc_info.value.message


@resp_lib.activate
def test_heimdal_create_sprint():
    resp_lib.add(resp_lib.POST, f"{BASE}/api/v1/auth/agent",
                 json={"token": "tok", "expires_at": 9999999999}, status=200)
    resp_lib.add(resp_lib.POST, f"{BASE}/api/v1/heimdal/sprints",
                 json={"id": 7, "requirement": "do the thing", "status": "pending"}, status=201)

    client = IdunaClient(BASE)
    client.auth_agent("EMILY_PRIME_COLAB_1", "secret")
    sprint = client.heimdal.create_sprint("do the thing", agent_name="EMILY_PRIME_COLAB_1")
    assert sprint["status"] == "pending"
    assert sprint["id"] == 7


@resp_lib.activate
def test_openapi_spec():
    resp_lib.add(resp_lib.GET, f"{BASE}/api/v1/openapi.json",
                 json={"openapi": "3.1.0", "info": {"title": "IDUNA IAM API"}},
                 status=200)
    client = IdunaClient(BASE)
    spec = client.openapi_spec()
    assert spec["openapi"] == "3.1.0"
