#!/usr/bin/env bash
set -euo pipefail

site_dir=${1:-site}
expected_routes=(/ /product/ /evidence/ /roadmap/ /docs/ /start/ /levels/ /comparison/)
expected_assets=(/images/studio-preview.png /images/studio-report.png)

for route in "${expected_routes[@]}"; do
  test -s "$site_dir/dist${route}index.html"
done
for asset in "${expected_assets[@]}"; do
  test -s "$site_dir/dist$asset"
done

for needle in \
  'Recovery is the proof' \
  'Built around the evidence' \
  'How deep did the' \
  'Make one drill' \
  'Project overview'; do
  grep -Rqs "$needle" "$site_dir/dist"
done

printf '%s\n' 'Checkride site route and asset checks passed.'
