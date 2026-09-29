"""LLM providers configured from environment variables.

Every provider speaks the OpenAI-compatible Chat Completions API, so one
client covers Gemini, OpenAI, OpenRouter and Ollama.
"""

from __future__ import annotations

import os
from dataclasses import dataclass


class UnknownProviderError(ValueError):
    pass


@dataclass(frozen=True)
class Provider:
    name: str
    base_url: str
    api_key: str
    default_model: str


@dataclass(frozen=True)
class _Known:
    name: str
    base_url: str
    key_var: str | None
    model_var: str
    fallback_model: str = ""


def _known() -> list[_Known]:
    return [
        _Known(
            "gemini",
            "https://generativelanguage.googleapis.com/v1beta/openai",
            "GEMINI_API_KEY",
            "GEMINI_MODEL",
            "gemini-flash-latest",
        ),
        _Known("openai", "https://api.openai.com/v1", "OPENAI_API_KEY", "OPENAI_MODEL"),
        _Known(
            "openrouter", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", "OPENROUTER_MODEL"
        ),
        # Local models need a base URL instead of a key.
        _Known("ollama", os.environ.get("OLLAMA_BASE_URL", ""), None, "OLLAMA_MODEL"),
    ]


class Registry:
    """The providers whose credentials are present in the environment."""

    def __init__(self, providers: list[Provider], default: str | None) -> None:
        self._providers = {p.name: p for p in providers}
        self.default = default

    @classmethod
    def from_env(cls) -> Registry:
        found: list[Provider] = []
        for k in _known():
            key = os.environ.get(k.key_var, "") if k.key_var else ""
            model = os.environ.get(k.model_var) or k.fallback_model
            if (k.key_var and not key) or not k.base_url or not model:
                continue
            found.append(Provider(k.name, k.base_url, key, model))
        default = found[0].name if found else None
        if os.environ.get("DEFAULT_PROVIDER") in {p.name for p in found}:
            default = os.environ["DEFAULT_PROVIDER"]
        return cls(found, default)

    def list(self) -> list[Provider]:
        return sorted(self._providers.values(), key=lambda p: p.name)

    def resolve(self, provider: str | None, model: str | None) -> tuple[Provider, str]:
        """Return the provider and model to use; empty arguments fall back to defaults."""
        name = provider or self.default
        if not name:
            raise UnknownProviderError("no LLM provider configured (set GEMINI_API_KEY)")
        if name not in self._providers:
            raise UnknownProviderError(f"unknown or unconfigured provider: {name!r}")
        p = self._providers[name]
        return p, model or p.default_model
