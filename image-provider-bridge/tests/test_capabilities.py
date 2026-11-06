import json
import subprocess
import sys
import pytest
from sub2_image_bridge.contract import Request, execute
from test_contract import payload, Session


@pytest.mark.parametrize('provider', ['wan', 'seedream'])
def test_capabilities_all_size_mappings_pass_original_body_validation(provider):
    session = Session()
    caps = execute(Request.parse({'provider': provider, 'operation': 'capabilities'}), session)
    assert session.calls == []
    assert len(caps['models']) == 2
    for model in caps['models']:
        raw = payload(provider)
        raw['model'] = model['model']
        raw['references'] = []
        assert model['default_image_size'] in model['image_sizes']
        assert len(model['sizes']) == 9 * len(model['image_sizes'])
        assert len({(s['image_size'], s['aspect_ratio']) for s in model['sizes']}) == len(model['sizes'])
        for row in model['sizes']:
            raw['parameters'] = {'profile_version': model['profile_version'], 'size': row['size']}
            body = Request.parse(raw).body()
            assert (body['parameters']['size'] if provider == 'wan' else body['size']) == row['size']
        raw['references'] = payload(provider)['references'] * model['max_reference_images']
        Request.parse(raw).body()
        assert model['response_mime_types'] == ['image/png', 'image/jpeg', 'image/webp']


@pytest.mark.parametrize('provider,expected', [
    ('wan', {'wan2.7-image': (['1K','2K'],9), 'wan2.7-image-pro': (['1K','2K'],9)}),
    ('seedream', {'doubao-seedream-5-0-lite-260128': (['2K','4K'],14), 'doubao-seedream-5-0-pro-260628': (['1K','2K'],10)}),
])
def test_module_capabilities_are_precise_without_credentials(provider, expected):
    run = subprocess.run([sys.executable, '-m', 'sub2_image_bridge'], input=json.dumps({'provider':provider,'operation':'capabilities'}).encode(), capture_output=True, check=True)
    assert run.stderr == b''
    caps = json.loads(run.stdout)
    assert {row['model']: (row['image_sizes'], row['max_reference_images']) for row in caps['models']} == expected
    for model in caps['models']:
        assert model['default_image_size'] == '2K'
        assert model['default_aspect_ratio'] == '1:1'
        square = next(row for row in model['sizes'] if row['image_size']=='2K' and row['aspect_ratio']=='1:1')
        assert square['size'] == ('2048*2048' if provider=='wan' else '2048x2048')
