#!/usr/bin/env bash
# Give an image that is already in the registry more tags, without rebuilding.
#
#   scripts/retag-image.sh IMAGE SOURCE_TAG NEW_TAG [NEW_TAG...]
#   scripts/retag-image.sh ghcr.io/ai-openshell/console sha-0a1b2c3 0.1.4 0.1
#
# CI builds one image per commit and tags it sha-<commit>. Every other tag is a
# statement about that same image, made later by something that knows more:
#
#   latest        ci.yml, once the whole pipeline has passed on the tip of main
#   X.Y.Z, X.Y    publish.yml, once a release vX.Y.Z has been cut at that commit
#
# Rebuilding for either would put a new image, built later, under a tag that
# claims to be the one from that commit. So both resolve the sha- tag to its
# digest and point the new tags at that digest.
#
# `docker buildx imagetools create` with a single source that is a manifest
# list (ours is: linux/amd64 + linux/arm64) "performs a carbon copy", in the
# words of its reference: the new tag gets the source's digest, and nothing
# moves but the manifest. The script checks that afterwards rather than
# trusting it.
#
# A version tag, X.Y.Z, is WRITE-ONCE. `latest` and X.Y are meant to move; a
# release is not. If X.Y.Z already names another digest the script fails and
# pushes nothing, not even the other tags it was given. That can really happen:
# "Re-run all jobs" on a released commit's CI run builds the commit again, the
# new build has a new digest (its labels carry the build time), and sha-<commit>
# is re-pointed at it. Without this rule the release's tag would follow, and
# anyone who had recorded the released digest would no longer match. The rule
# goes by the shape of the tag, not by a flag, so no caller can forget it.
# Re-tagging X.Y.Z with the digest it already has is fine and does nothing.
#
# DRY_RUN=1 resolves the digests and prints the command without running it.
# Needs docker buildx, jq, and a login that can push to IMAGE.
set -euo pipefail

if [ "$#" -lt 3 ]; then
  echo "usage: $0 IMAGE SOURCE_TAG NEW_TAG [NEW_TAG...]" >&2
  exit 2
fi

image="$1"
source_tag="$2"
shift 2

inspect_err="$(mktemp)"
trap 'rm -f "$inspect_err"' EXIT

digest_of() {
  docker buildx imagetools inspect "$1" --format '{{json .Manifest}}' | jq -er '.digest'
}

# Prints the digest a reference names, or nothing when the registry says there
# is no such tag. Fails on any other answer: a registry that is down or a login
# that has expired is not "the tag is free", and treating it as such is exactly
# how a write-once tag would get overwritten.
#
# buildx reports a missing tag as "ERROR: <reference>: not found": the
# registry's 404, as worded by the containerd resolver it uses. Only a line
# ending that way counts. Anything else it says is some other failure, and so
# is "docker: command not found".
digest_if_present() {
  local manifest
  if manifest="$(docker buildx imagetools inspect "$1" --format '{{json .Manifest}}' 2>"$inspect_err")"; then
    jq -er '.digest' <<< "$manifest"
    return
  fi
  if grep -Eq ': not found[[:space:]]*$' "$inspect_err"; then
    return 0
  fi
  cat "$inspect_err" >&2
  return 1
}

is_version_tag() {
  [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
}

if ! digest="$(digest_of "${image}:${source_tag}")"; then
  echo "::error::${image}:${source_tag} is not in the registry. That tag is pushed by the" \
       "push-manifest job in ci.yml, so CI has to have built this commit first." >&2
  exit 1
fi
case "$digest" in
  sha256:*) ;;
  *)
    echo "::error::could not read a digest for ${image}:${source_tag} (got '${digest}')" >&2
    exit 1
    ;;
esac
echo "${image}:${source_tag} is ${digest}"

# Every version tag is checked before anything is pushed, so a refusal leaves
# the registry exactly as it was.
tag_args=()
for tag in "$@"; do
  if is_version_tag "$tag"; then
    if ! existing="$(digest_if_present "${image}:${tag}")"; then
      echo "::error::could not tell whether ${image}:${tag} already exists (the registry's answer" \
           "is above). A version tag is write-once, so it is not pushed blind. Nothing was" \
           "pushed; run this again." >&2
      exit 1
    fi
    if [ -n "$existing" ] && [ "$existing" != "$digest" ]; then
      echo "::error::${image}:${tag} already names ${existing}, and a version tag is never moved." \
           "${image}:${source_tag} is now ${digest}. That is what it looks like when a commit" \
           "is built again after it was released, which a full re-run of its CI run does." \
           "Nothing was pushed, and ${tag} still names what it named before, so if that is" \
           "what happened there is nothing to repair." >&2
      exit 1
    fi
    if [ -n "$existing" ]; then
      echo "${image}:${tag} already names ${digest}; leaving it"
      continue
    fi
  fi
  tag_args+=(--tag "${image}:${tag}")
done

if [ "${#tag_args[@]}" -eq 0 ]; then
  echo "every tag asked for is already in place; nothing to push"
else
  echo "+ docker buildx imagetools create ${tag_args[*]} ${image}@${digest}"
  if [ "${DRY_RUN:-}" = "1" ]; then
    echo "DRY_RUN=1: not pushing"
    exit 0
  fi
  docker buildx imagetools create "${tag_args[@]}" "${image}@${digest}"
fi

# A carbon copy has the source's digest. Anything else means the registry now
# holds a different manifest under a tag that claims to be this commit's image.
for tag in "$@"; do
  pushed="$(digest_of "${image}:${tag}")"
  if [ "$pushed" != "$digest" ]; then
    echo "::error::${image}:${tag} is ${pushed}, expected ${digest}" >&2
    exit 1
  fi
  echo "${image}:${tag} -> ${digest}"
done
