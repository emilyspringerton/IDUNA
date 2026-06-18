from setuptools import setup, find_packages

setup(
    name="einhorn_sdk",
    version="0.1.0",
    description="Python SDK for the EINHORN_INDUSTRIAL stack (IDUNA IAM + Apples + Drive + HEIMDAL)",
    author="Emily Springerton",
    author_email="emilyspringerton@gmail.com",
    packages=find_packages(),
    python_requires=">=3.9",
    install_requires=[
        "requests>=2.28.0",
    ],
    extras_require={
        "colab": [],  # google-colab is pre-installed in Colab; no extra deps
        "dev": ["pytest", "responses"],
    },
)
