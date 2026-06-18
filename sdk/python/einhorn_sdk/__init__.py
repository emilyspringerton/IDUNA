"""
einhorn_sdk — Python SDK for the EINHORN_INDUSTRIAL stack.

Wraps the IDUNA IAM API (OpenAPI 3.1 at /api/v1/openapi.json).
Designed to run in Google Colab, Jupyter, or any Python 3.9+ environment.

Quickstart (Colab):
    from einhorn_sdk import IdunaClient

    client = IdunaClient("http://iduna.farthq.internal:8080")
    client.auth_local("webmaster@localhost", "your-password")

    # Post an Apple from Colab
    apple = client.apples.post("observation", "Colab GPU run complete", body="Loss: 0.42")

    # File a HEIMDAL sprint
    sprint = client.heimdal.create_sprint("Train GPT-2 for 1 epoch on T4 GPU")

    # Upload training artifact to Drive
    with open("model.tar.gz", "rb") as f:
        client.drive.upload("model.tar.gz", f.read(), mime_type="application/gzip")
"""

from .client import IdunaClient  # noqa: F401

__version__ = "0.1.0"
__all__ = ["IdunaClient"]
