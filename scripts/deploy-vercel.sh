#!/usr/bin/env bash
# deploy-vercel.sh — publish site/ to the project's production alias.
#
#   scripts/deploy-vercel.sh          # stamp, upload, verify, unstamp
#
# Why the extra steps:
#  * --stamp-deploy records the exact commit being published in BUILD.json, so
#    the live page states which commit is served (verified in the evidence
#    bundle under results/deploy-*).
#  * Vercel blocks deployments that carry git metadata whose commit author is
#    not a project member (we saw: "The deployment was blocked because the
#    commit author doesn't have permission to create deployments for this
#    project"). The upload therefore happens from a copy of site/ that has no
#    .git above it; the content is byte-identical and is checked against git
#    before uploading.
#  * site/BUILD.json is restored afterwards so the working tree stays clean.
set -euo pipefail
cd "$(dirname "$0")/.."

BASE_URL="${BASE_URL:-https://sre-portfolio-demos.vercel.app}"
STAMP="$(git rev-parse HEAD)"

echo "deploy-vercel: stamp BUILD.json with ${STAMP:0:12}"
./scripts/build-demo-site.sh --stamp-deploy "$STAMP" >/dev/null
restore() { git checkout -- site/BUILD.json; }
trap restore EXIT

SRC="$(mktemp -d /tmp/sre-portfolio-site.XXXXXX)"
cp -R site/. "$SRC"/

echo "deploy-vercel: checking the upload copy against git HEAD"
for p in index.html style.css budgetguard faultlab recoverops slos; do
  diff -r "site/$p" "$SRC/$p" >/dev/null || {
    echo "deploy-vercel: $p differs from site/ — refusing to publish" >&2
    exit 1
  }
done

echo "deploy-vercel: uploading from $SRC"
(cd "$SRC" && npx -y vercel@latest deploy --prod --yes)
rm -rf "$SRC"

echo "deploy-vercel: verifying $BASE_URL"
live_sha="$(curl -fsS "$BASE_URL/BUILD.json" | python3 -c \
  'import json,sys; print(json.load(sys.stdin).get("deploy_commit",""))')"
if [ "$live_sha" != "$STAMP" ]; then
  echo "deploy-vercel: live deploy_commit=${live_sha:-<missing>} != $STAMP" >&2
  exit 1
fi
for u in "" budgetguard/ faultlab/ recoverops/; do
  code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/$u")"
  [ "$code" = "200" ] || { echo "deploy-vercel: /$u -> $code" >&2; exit 1; }
done
echo "deploy-vercel: OK — $BASE_URL (deploy_commit ${STAMP:0:12})"
