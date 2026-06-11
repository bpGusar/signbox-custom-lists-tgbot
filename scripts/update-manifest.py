#!/usr/bin/env python3
import json
import sys


def main() -> None:
    version, release, path = sys.argv[1], sys.argv[2], sys.argv[3]
    with open(path) as f:
        data = json.load(f)
    data["version"] = f"{version}-r{release}"
    data["packages"]["lst-signbox-lists-tgbot"] = f"lst-signbox-lists-tgbot_{version}-r{release}_${{ARCH}}.ipk"
    data["packages"]["luci-app-lst-signbox-lists-tgbot"] = f"luci-app-lst-signbox-lists-tgbot_{version}-r{release}_all.ipk"
    with open(path, "w") as f:
        json.dump(data, f, indent=2)
        f.write("\n")


if __name__ == "__main__":
    main()
