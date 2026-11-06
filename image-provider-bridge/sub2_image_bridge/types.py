"""Minimal credential and error interface; no scheduling or pricing state."""
from dataclasses import dataclass
from enum import Enum

@dataclass(frozen=True, slots=True)
class Credential:
    endpoint_url: str
    api_key: str
    timeout_seconds: int = 120

class ProviderPhaseContractError(RuntimeError):
    retryable = False

class ProviderPhaseAdapterError(RuntimeError):
    def __init__(self, message: str, *, retryable: bool):
        super().__init__(message)
        self.retryable = retryable

class ProviderSubmissionDiagnosticCategory(str, Enum):
    TRANSPORT_EXCEPTION = "TRANSPORT_EXCEPTION"
    HTTP_5XX = "HTTP_5XX"
    HTTP_2XX_IMAGE_MISSING = "HTTP_2XX_IMAGE_MISSING"
    HTTP_2XX_IMAGE_BASE64_INVALID = "HTTP_2XX_IMAGE_BASE64_INVALID"
    HTTP_2XX_IMAGE_MIME_INVALID = "HTTP_2XX_IMAGE_MIME_INVALID"
    HTTP_2XX_IMAGE_SIZE_INVALID = "HTTP_2XX_IMAGE_SIZE_INVALID"
