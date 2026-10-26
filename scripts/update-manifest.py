#!/usr/bin/env python3
import json
import sys


def main() -> None:
    version, release, path = sys.argv[1], sys.argv[2], sys.argv[3]
    with open(path) as f:
        data = json.load(f)
    full = f"{version}-r{release}"
    data["version"] = full
    data["packages"]["lst-signbox-lists-tgbot"] = f"lst-signbox-lists-tgbot_{full}_${{ARCH}}.ipk"
    data["packages"]["luci-app-lst-signbox-lists-tgbot"] = f"luci-app-lst-signbox-lists-tgbot_{full}_all.ipk"
    data["apk_openwrt_version"] = "25.12"
    data["apk_packages"] = {
        "lst-signbox-lists-tgbot": f"lst-signbox-lists-tgbot_{full}_${{ARCH}}.apk",
        "luci-app-lst-signbox-lists-tgbot": f"luci-app-lst-signbox-lists-tgbot_{full}_all.apk",
    }
    architectures = data.pop("architectures", None)
    if architectures is not None:
        data["architectures"] = architectures
    with open(path, "w") as f:
        json.dump(data, f, indent=2)
        f.write("\n")


if __name__ == "__main__":
    main()
