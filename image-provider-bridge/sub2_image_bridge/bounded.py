"""Apply a transport byte ceiling without changing vendored HTTP semantics."""
import requests

MAX_JSON_BYTES = 34 * 1024 * 1024

class BoundedSession(requests.Session):
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
            return response
        finally:
            response.close()
