from .seedream_model_policy import (
    seedream_maximum_references,
    seedream_model_allowed_for_profile,
    seedream_requires_png,
    seedream_supports_output_size,
    seedream_supports_sequential_image_generation,
)
from .seedream import (
    _SEEDREAM_LEGACY_BODY_FIELDS,
    _SEEDREAM_PRO_BODY_FIELDS,
    _SEEDREAM_SEQUENTIAL_PRO_BODY_FIELDS,
)
from .types import ProviderPhaseContractError

def validate_body(body, profile_version):
    model = body.get("model") if isinstance(body, dict) else None
    supports_sequential_configuration = (
        seedream_supports_sequential_image_generation(
            model,
            profile_version,
        )
    )
    body_fields = (
        _SEEDREAM_SEQUENTIAL_PRO_BODY_FIELDS
        if seedream_requires_png(model)
        and supports_sequential_configuration
        else _SEEDREAM_PRO_BODY_FIELDS
        if seedream_requires_png(model)
        else _SEEDREAM_LEGACY_BODY_FIELDS
    )
    if "image" not in body:
        body_fields = body_fields - {"image"}
    if (
        not seedream_model_allowed_for_profile(
            model,
            profile_version,
        )
        or frozenset(body) != body_fields
        or not seedream_supports_output_size(
            model,
            profile_version,
            body.get("size"),
        )
        or (
            seedream_requires_png(model)
            and body.get("output_format") != "png"
        )
        or body.get("response_format") != "b64_json"
        or (
            supports_sequential_configuration
            and body.get("sequential_image_generation")
            != "disabled"
        )
        or body.get("watermark") is not False
        or not isinstance(body.get("prompt"), str)
        or not body["prompt"].strip()
        or not isinstance(body.get("image", []), list)
        or not 0 <= len(body.get("image", [])) <= seedream_maximum_references(
            model,
            profile_version,
        )
    ):
        raise ProviderPhaseContractError(
            "Seedream frozen submit envelope is invalid"
        )
