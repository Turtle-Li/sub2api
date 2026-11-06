from __future__ import annotations

import re
from typing import Final


SEEDREAM_LITE_MODEL: Final = "doubao-seedream-5-0-lite-260128"
SEEDREAM_PRO_MODEL: Final = "doubao-seedream-5-0-pro-260628"
SEEDREAM_LEGACY_PRO_MODEL: Final = "doubao-seedream-5-0-260128"
SEEDREAM_DEFAULT_MAXIMUM_REFERENCES: Final = 14
SEEDREAM_PRO_MAXIMUM_REFERENCES: Final = 10

_CURRENT_LITE_MODEL = re.compile(
    r"^doubao-seedream-5-0-lite-[0-9]{6}$"
)
_CURRENT_PRO_MODEL = re.compile(
    r"^doubao-seedream-5-0-pro-[0-9]{6}$"
)
_PROFILE_V1_MODELS = frozenset({
    SEEDREAM_LITE_MODEL,
    SEEDREAM_LEGACY_PRO_MODEL,
})
_SEEDREAM_ONE_K_SIZES = frozenset({
    "1024x1024", "960x1440", "1440x960", "960x1280",
    "1280x960", "960x1200", "1200x960", "720x1280",
    "1280x720",
})
_SEEDREAM_TWO_K_SIZES = frozenset({
    "2048x2048", "1664x2496", "2496x1664", "1728x2304",
    "2304x1728", "1920x2400", "2400x1920", "1440x2560",
    "2560x1440",
})
_SEEDREAM_FOUR_K_SIZES = frozenset({
    "4096x4096", "2720x4080", "4080x2720", "3072x4096",
    "4096x3072", "3264x4080", "4080x3264", "2304x4096",
    "4096x2304",
})


def normalized_seedream_model(value: object) -> str:
    return str(value or "").strip().lower()


def current_seedream_model_tier(value: object) -> str | None:
    model = normalized_seedream_model(value)
    if _CURRENT_LITE_MODEL.fullmatch(model):
        return "LITE"
    if _CURRENT_PRO_MODEL.fullmatch(model):
        return "PRO"
    return None


def seedream_runtime_model(value: object) -> bool:
    model = normalized_seedream_model(value)
    return (
        current_seedream_model_tier(model) is not None
        or model == SEEDREAM_LEGACY_PRO_MODEL
    )


def seedream_model_allowed_for_profile(
    value: object,
    profile_version: object,
) -> bool:
    model = normalized_seedream_model(value)
    version = str(profile_version or "").strip()
    if version == "1":
        return model in _PROFILE_V1_MODELS
    if version == "2":
        # Profile v2 was already the default before the legacy Pro ID was
        # retired from new routes. Frozen v2 jobs must remain executable.
        return (
            current_seedream_model_tier(model) is not None
            or model == SEEDREAM_LEGACY_PRO_MODEL
        )
    if version in {"3", "4"}:
        return current_seedream_model_tier(model) is not None
    return False


def seedream_requires_png(value: object) -> bool:
    return current_seedream_model_tier(value) == "PRO"


def seedream_supports_sequential_image_generation(
    value: object,
    profile_version: object,
) -> bool:
    if not seedream_model_allowed_for_profile(value, profile_version):
        return False
    return (
        str(profile_version or "").strip() not in {"3", "4"}
        or not seedream_requires_png(value)
    )


def seedream_supports_output_resolution(
    value: object,
    profile_version: object,
    resolution: object,
) -> bool:
    if not seedream_model_allowed_for_profile(value, profile_version):
        return False
    size = str(resolution or "").strip().upper()
    version = str(profile_version or "").strip()
    if seedream_requires_png(value):
        if version == "2":
            return size in {"2K", "4K"}
        if version in {"3", "4"}:
            return size in {"1K", "2K"}
        return False
    if version == "1":
        return size in {"1K", "2K", "4K"}
    if version in {"2", "3", "4"}:
        return size in {"2K", "4K"}
    return False


def seedream_supports_output_size(
    value: object,
    profile_version: object,
    size: object,
) -> bool:
    version = str(profile_version or "").strip()
    normalized_size = str(size or "").strip()
    if version != "4":
        return seedream_supports_output_resolution(
            value,
            profile_version,
            normalized_size,
        )
    if not seedream_model_allowed_for_profile(value, profile_version):
        return False
    if normalized_size.upper() in {"1K", "2K", "4K"}:
        return seedream_supports_output_resolution(
            value,
            profile_version,
            normalized_size,
        )
    if seedream_requires_png(value):
        return (
            normalized_size in _SEEDREAM_ONE_K_SIZES
            or normalized_size in _SEEDREAM_TWO_K_SIZES
        )
    return current_seedream_model_tier(value) == "LITE" and (
        normalized_size in _SEEDREAM_TWO_K_SIZES
        or normalized_size in _SEEDREAM_FOUR_K_SIZES
    )


def seedream_supports_native_size_for_resolution(
    value: object,
    profile_version: object,
    resolution: object,
    size: object,
) -> bool:
    """Validate a concrete v4 pixel bucket against its tier.

    ``seedream_supports_output_size`` intentionally answers only whether a
    size is legal for a profile. Native delivery also needs to prove that a
    returned custom size belongs to the requested 1K/2K/4K bucket.
    """
    normalized_resolution = str(resolution or "").strip().upper()
    normalized_size = str(size or "").strip()
    if normalized_size.upper() in {"1K", "2K", "4K"}:
        return normalized_size.upper() == normalized_resolution and (
            seedream_supports_output_resolution(
                value, profile_version, normalized_resolution
            )
        )
    if str(profile_version or "").strip() != "4":
        return False
    if not seedream_model_allowed_for_profile(value, profile_version):
        return False
    if normalized_resolution == "1K":
        return seedream_requires_png(value) and normalized_size in _SEEDREAM_ONE_K_SIZES
    if normalized_resolution == "2K":
        return normalized_size in _SEEDREAM_TWO_K_SIZES and (
            seedream_requires_png(value) or current_seedream_model_tier(value) == "LITE"
        )
    if normalized_resolution == "4K":
        return (
            current_seedream_model_tier(value) == "LITE"
            and normalized_size in _SEEDREAM_FOUR_K_SIZES
        )
    return False


def seedream_maximum_references(
    value: object,
    profile_version: object,
) -> int:
    if not seedream_model_allowed_for_profile(value, profile_version):
        raise ValueError("Seedream model is outside the approved profile")
    if (
        str(profile_version or "").strip() in {"3", "4"}
        and seedream_requires_png(value)
    ):
        return SEEDREAM_PRO_MAXIMUM_REFERENCES
    if seedream_runtime_model(value):
        return SEEDREAM_DEFAULT_MAXIMUM_REFERENCES
    raise ValueError("Seedream model is outside the approved contract")
