# Native output fixture

`native-output.webp` is an original 2×2 lossless WebP fixture with alternating
blue and pale pixels. It was generated locally with `cwebp -lossless` from a P6
PPM containing RGB pixels (0,80,200), (240,250,255), (240,250,255), (0,80,200).
No upstream artwork or third-party licensed test image was copied.

The native Muse task fixture returns these original WebP bytes in `b64_json`,
matching pinned `app.py::_image_response`, which does not transcode image data.
The test verifies full decoding, actual MIME propagation through the combined
batch result, a single POST across restart, and corrupt/oversized rejection.
