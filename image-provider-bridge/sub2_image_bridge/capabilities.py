"""Read-only projection of the pinned provider policies for Sub2 admission."""
from fractions import Fraction
from typing import Final, TypedDict
from . import seedream_model_policy as seedream, wan


class SizeMapping(TypedDict):
    image_size: str
    aspect_ratio: str
    size: str


class ModelCapability(TypedDict):
    model: str
    profile_version: str
    image_sizes: list[str]
    default_image_size: str
    default_aspect_ratio: str
    max_reference_images: int
    response_mime_types: list[str]
    sizes: list[SizeMapping]


# Public ratio labels classify source-approved integer dimensions; they never
# generate a new dimension. Pixel rounding may make the exact ratio differ.
ASPECT_RATIOS: Final = ('1:1', '2:3', '3:2', '3:4', '4:3', '4:5', '5:4', '9:16', '16:9')
RESPONSE_MIME_TYPES: Final = ('image/png', 'image/jpeg', 'image/webp')


def _ratio(size: str) -> str:
    width, height = (int(value) for value in size.replace('*', 'x').split('x'))
    actual = Fraction(width, height)
    label = min(ASPECT_RATIOS, key=lambda ratio: abs(actual - Fraction(ratio.replace(':', '/'))))
    if abs(actual - Fraction(label.replace(':', '/'))) > Fraction(1, 500):
        raise RuntimeError('Source dimension has no supported public aspect ratio')
    return label


def model_capabilities(provider: str) -> list[ModelCapability]:
    """Expose default-profile models, sizes and bounds without credentials or IO."""
    models = sorted(wan.WAN_MODELS) if provider == 'wan' else [seedream.SEEDREAM_LITE_MODEL, seedream.SEEDREAM_PRO_MODEL]
    version = '2' if provider == 'wan' else '4'
    source_sizes = (
        {'1K': wan._WAN_ONE_K_SIZES, '2K': wan._WAN_TWO_K_SIZES}
        if provider == 'wan' else
        {'1K': seedream._SEEDREAM_ONE_K_SIZES, '2K': seedream._SEEDREAM_TWO_K_SIZES, '4K': seedream._SEEDREAM_FOUR_K_SIZES}
    )
    result: list[ModelCapability] = []
    for model in models:
        sizes: list[SizeMapping] = []
        for tier, candidates in source_sizes.items():
            for size in sorted(candidates):
                supported = (
                    wan._approved_wan_size(version, size)
                    if provider == 'wan' else
                    seedream.seedream_supports_native_size_for_resolution(model, version, tier, size)
                    and seedream.seedream_supports_output_size(model, version, size)
                )
                if supported:
                    sizes.append({'image_size': tier, 'aspect_ratio': _ratio(size), 'size': size})
        tiers = [tier for tier in source_sizes if any(row['image_size'] == tier for row in sizes)]
        if provider == 'wan':
            # Ask the preserved content predicate, rather than copy its limit.
            reference = {'image': 'data:image/png;base64,AAAA'}
            maximum = max(count for count in range(1, 15) if wan._approved_wan_content([reference] * count + [{'text': 'capability'}]))
        else:
            maximum = seedream.seedream_maximum_references(model, version)
        result.append({
            'model': model, 'profile_version': version,
            'image_sizes': tiers, 'default_image_size': '2K' if '2K' in tiers else tiers[0],
            'default_aspect_ratio': '1:1', 'max_reference_images': maximum,
            'response_mime_types': list(RESPONSE_MIME_TYPES), 'sizes': sizes,
        })
    return result
