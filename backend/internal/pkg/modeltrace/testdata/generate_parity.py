"""Generate golden results from an unmodified ModelTrace checkout.

Usage: python generate_parity.py E:/codes/ModelTrace
Reference commit: 6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2
"""
import json
import random
import sys
from pathlib import Path

sys.path.insert(0, sys.argv[1])
import fingerprint

bank = fingerprint.load_bank(Path(sys.argv[1]) / "data/unified_bank.json")
rng = random.Random(20261012)
cases = []

def add(name, outputs):
    try:
        result = fingerprint.analyze_global_outputs(outputs, bank)
    except ValueError:
        result = None
    cases.append({"name": name, "outputs": outputs, "expected": result})

for count in (1, 2, 3, 5):
    outputs = []
    for index in range(count):
        nums = [rng.randint(1, 355) for _ in range(292 + index)]
        outputs.append({"text": " ".join(map(str, nums)), "expected_count": 300})
    add(f"random-{count}-outputs", outputs)

for entry in bank["models"]:
    nums = rng.choices(range(1, 356), weights=entry["counts"], k=311)
    add("profile-" + entry["id"], [{"text": ",".join(map(str, nums)), "expected_count": 311}])

for length in (164, 165, 166, 291, 292, 293, 332):
    text = " ".join(str((i * 37 + 13) % 355 + 1) for i in range(length))
    add(f"threshold-split-{length}", [{"text": text, "expected_count": 300}])

text = " ".join(str((i * 17) % 355 + 1) for i in range(300))
add("unicode-decimals", [{"text": text.translate(str.maketrans("0123456789", "０１２３４５６７８９")), "expected_count": 300}])
add("filtered-runs", [{"text": "1 2 3 commentary " + text + " 999999999999999999999999999999999999999 355", "expected_count": 300}])
add("mixed-invalid", [{"text": "refused", "expected_count": 300}, {"text": text, "expected_count": 300}])

parsers = ["12,85,340,999,1,355 and 42", "１ ٢ ३ 355", "1 2 " + "9" * 100 + " words 3 4 5", "no numbers"]
fixture = {"reference_commit": "6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2", "cases": cases,
           "parsers": [{"text": text, "numbers": fingerprint.parse_numbers(text)} for text in parsers]}
Path(__file__).with_name("python_parity.json").write_text(json.dumps(fixture, ensure_ascii=False, indent=2), encoding="utf-8")
