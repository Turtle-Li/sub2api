"""Apply a byte ceiling and report explicit rejections without replaying POSTs."""
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from math import ceil

import requests

MAX_JSON_BYTES = 34 * 1024 * 1024


class TemporarySubmitRejection(Exception):
    def __init__(self, retry_after_seconds: int):
        super().__init__('temporary submit rejection')
        self.retry_after_seconds = retry_after_seconds


def _retry_after_seconds(raw: str) -> int:
    # A long hint stays long on the wire so Go declines a premature retry.
    cap = 86400
    raw = raw.strip()
    if raw.isascii() and raw.isdecimal():
        digits = raw.lstrip('0')
        return cap if len(digits) > 5 else min(cap, int(digits or '0'))
    try:
        date = parsedate_to_datetime(raw)
        if date.tzinfo is None:
            date = date.replace(tzinfo=timezone.utc)
        return min(cap, max(0, ceil((date - datetime.now(timezone.utc)).total_seconds())))
    except (ValueError, TypeError, OverflowError):
        return 0


def _is_temporary_submit_rejection(provider, response) -> bool:
    if response.status_code != 429:
        return False
    try:
        body = response.json()
    except ValueError:
        return False
    if not isinstance(body, dict) or any(key in body for key in ('data', 'output', 'task_id')):
        return False
    if provider == 'wan':
        return body.get('code') in ('Throttling.RateQuota', 'Throttling.BurstRate', 'Throttling.Concurrency')
    error = body.get('error')
    return provider == 'seedream' and isinstance(error, dict) and error.get('code') in (
        'ModelAccountIpmRateLimitExceeded', 'ModelAccountRpmRateLimitExceeded',
        'RateLimitExceeded.EndpointRPMExceeded', 'APIAccountRpmRateLimitExceeded', 'ServerOverloaded',
    )


class BoundedSession(requests.Session):
    def __init__(self, *, submit_provider: str = ''):
        super().__init__()
        self.submit_provider = submit_provider

    def request(self, method, url, **kwargs):
        streaming = kwargs.get("stream", False)
        kwargs["stream"] = True
        response = super().request(method, url, **kwargs)
        if streaming:
            return response
        try:
            chunks = []
            size = 0
            for chunk in response.iter_content(64 * 1024):
                size += len(chunk)
                if size > MAX_JSON_BYTES:
                    raise requests.RequestException("response byte ceiling")
                chunks.append(chunk)
            response._content = b"".join(chunks)
            response._content_consumed = True
            if self.submit_provider and method.upper() == 'POST' and _is_temporary_submit_rejection(self.submit_provider, response):
                raise TemporarySubmitRejection(_retry_after_seconds(response.headers.get('Retry-After', '')))
            return response
        finally:
            response.close()
