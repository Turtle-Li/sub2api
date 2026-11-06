from .wan import WAN_MODELS, _approved_wan_size, _approved_wan_content
from .types import ProviderPhaseContractError

def validate_body(body, profile_version):
    model = str(body.get("model") or "")
    parameters = body.get("parameters")
    messages = (body.get("input") or {}).get("messages")
    if (
        model not in WAN_MODELS
        or not isinstance(parameters, dict)
        or not _approved_wan_size(
            profile_version,
            parameters.get("size"),
        )
        or parameters.get("n") != 1
        or parameters.get("watermark") is not False
        or parameters.get("enable_sequential") is not False
        or not isinstance(messages, list)
        or len(messages) != 1
        or messages[0].get("role") != "user"
        or not (_approved_wan_content(messages[0].get("content"))
                or _approved_text_only(messages[0].get("content")))
    ):
        raise ProviderPhaseContractError(
            "Wan frozen submit body is outside the approved contract"
        )


def _approved_text_only(content):
    """Wan official text-to-image shape: one text item without image items."""
    return (isinstance(content, list) and len(content) == 1
            and isinstance(content[0], dict) and set(content[0]) == {"text"}
            and isinstance(content[0]["text"], str)
            and bool(content[0]["text"].strip())
            and len(content[0]["text"]) <= 6000)
