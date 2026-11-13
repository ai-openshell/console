#!/usr/bin/env bash
# Sign in to quay.io as a robot account with no stored secret.
#
#   scripts/quay-login.sh ROBOT
#   scripts/quay-login.sh ai-openshell+console_release
#
# The job asks GitHub for its OIDC token (it needs `permissions: id-token:
# write`) and hands it to Quay's robot federation endpoint, which answers with a
# robot token that lasts one hour. Quay accepts the GitHub token only when the
# robot trusts its issuer (https://token.actions.githubusercontent.com) and its
# subject exactly. The robots trust a GitHub environment, for example
# repo:ai-openshell/console:environment:release, so a job outside that
# environment gets nothing, whatever the workflow file says, and a job in it
# can write only the repositories the robot has write on.
#
# Then docker, and helm when it is installed, are logged in with that token.
#
# QUAY_REGISTRY (default quay.io) and QUAY_AUDIENCE (default the registry)
# override the host and the audience of the GitHub token. Quay checks the
# audience only when the robot's federation lists one.
set -euo pipefail

robot="${1:?usage: scripts/quay-login.sh ROBOT (namespace+name)}"
registry="${QUAY_REGISTRY:-quay.io}"
audience="${QUAY_AUDIENCE:-$registry}"

if [[ -z "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" || -z "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:-}" ]]; then
  echo "quay-login: no GitHub OIDC token available; the job needs 'permissions: id-token: write'" >&2
  exit 1
fi

jwt="$(curl --fail-with-body -sS \
  -H "Authorization: bearer ${ACTIONS_ID_TOKEN_REQUEST_TOKEN}" \
  "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=${audience}" | jq -r '.value // empty')"
if [[ -z "$jwt" ]]; then
  echo "quay-login: GitHub returned no OIDC token" >&2
  exit 1
fi
echo "::add-mask::${jwt}"

# A failed exchange prints Quay's reason (an unknown robot, an issuer or a
# subject the robot does not trust) and never the token.
if ! answer="$(curl --fail-with-body -sS -u "${robot}:${jwt}" \
  "https://${registry}/oauth2/federation/robot/token")"; then
  echo "quay-login: ${registry} refused the exchange for ${robot}: ${answer}" >&2
  exit 1
fi
token="$(jq -r '.token // empty' <<< "$answer")"
if [[ -z "$token" ]]; then
  echo "quay-login: ${registry} returned no token for ${robot}" >&2
  exit 1
fi
echo "::add-mask::${token}"

docker login "$registry" -u "$robot" --password-stdin <<< "$token"
if command -v helm > /dev/null; then
  helm registry login "$registry" -u "$robot" --password-stdin <<< "$token"
fi
