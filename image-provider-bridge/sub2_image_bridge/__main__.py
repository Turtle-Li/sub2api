"""Bounded JSON stdin/stdout protocol. Credentials never enter argv or logs."""
import json
import sys
from .bounded import BoundedSession, MAX_JSON_BYTES
from .contract import Request, InvalidRequest, execute, error
from .types import ProviderPhaseContractError

MAX_INPUT_BYTES = 48 * 1024 * 1024

def main():
    try:
        payload = sys.stdin.buffer.read(MAX_INPUT_BYTES + 1)
        if len(payload) > MAX_INPUT_BYTES:
            raise InvalidRequest()
        request = Request.parse(json.loads(payload))
    except (InvalidRequest,ProviderPhaseContractError,ValueError,TypeError,UnicodeError,RecursionError):
        response = error('rejected','invalid_request')
    else:
        try:
            with BoundedSession() as session:
                response = execute(request,session)
        except (ValueError,TypeError,UnicodeError,RecursionError):
            response = error('outcome_unknown','invalid_provider_response')
    encoded = json.dumps(response,separators=(',',':'),ensure_ascii=True)
    if len(encoded) > MAX_JSON_BYTES:
        encoded = json.dumps(error('outcome_unknown','output_limit'))
    sys.stdout.write(encoded+'\n')

if __name__ == '__main__':
    main()
