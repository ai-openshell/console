#!/usr/bin/env bash
# The local development stack: the OpenShell gateway this branch pins
# (deploy/ci/gateway-pins.json), in Docker, and with --sign-in a Keycloak
# holding the realm of deploy/keycloak/openshell-realm.json.
#
#   scripts/dev-env.sh up              the gateway              (make gateway)
#   scripts/dev-env.sh up --sign-in    Keycloak and the gateway (make dev-full)
#   scripts/dev-env.sh status          what is running, the addresses, the two users and their password
#   scripts/dev-env.sh logs            follow the gateway's and Keycloak's logs  (make dev-logs)
#   scripts/dev-env.sh down            stop it; the gateway's data stays         (make dev-down)
#   scripts/dev-env.sh reset           stop it, delete its data and its sandboxes (make dev-reset)
#
# make dev, make dev-full and the compat targets first call `ports`, which
# stops when a port they listen on is taken. make dev and make dev-full take
# the BFF's settings from `env`, and make dev-full runs `proxy` (the auth
# proxy, on this machine, in front of the UI dev server). The compat targets
# end with `compat-clean`, which removes what their gateway kept.
#
# The stack is deploy/dev/compose.yaml. With sign-in it also serves make dev:
# the gateway accepts a call without a token and checks one that is sent.
#
# Needs docker with compose, jq, openssl and curl; `proxy` needs go.
# Environment:
#   DEV_STATE_DIR   where the generated files go (default scripts/.state/local).
#                   The Docker daemon must be able to mount it: with colima, a
#                   directory under your home directory.
#   DOCKER_SOCK     the Docker socket as the daemon sees it (default
#                   /var/run/docker.sock).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
PINS="$ROOT/deploy/ci/gateway-pins.json"
REALM="$ROOT/deploy/keycloak/openshell-realm.json"
COMPOSE_FILE="$ROOT/deploy/dev/compose.yaml"

export DEV_STATE_DIR="${DEV_STATE_DIR:-$ROOT/scripts/.state/local}"
STATE="$DEV_STATE_DIR"

# One Compose project per checkout. A second checkout then stops at a port
# that is in use, and does not replace this one's containers. The name comes
# from the checkout's path: `reset` before the checkout is deleted or moved,
# or the gateway's data and its sandboxes stay in Docker under this name.
PROJECT="console-dev-$(printf '%s' "$ROOT" | cksum | cut -d' ' -f1)"

# The gateway marks the containers and volumes of its sandboxes with this
# label, and treats everything that carries it as its own: when it starts, it
# removes the supervisor of every sandbox with its label. With a label of its
# own, this gateway and another one on the same Docker daemon (the compat
# suite's, for one) leave each other's sandboxes alone.
SANDBOX_LABEL="openshell.ai/sandbox-namespace=${PROJECT}"

# The ports on this machine. They are set here and nowhere else:
# deploy/dev/compose.yaml and deploy/dev/realm-local.jq are given them.
GATEWAY_PORT=50051
HEALTH_PORT=50053
KEYCLOAK_PORT=8180
PROXY_PORT=4180
UI_PORT=3000             # frontend/vite.config.ts
BFF_PORT="${PORT:-8080}" # the BFF reads PORT itself

# What deploy/keycloak/openshell-realm.json calls its realm and the
# dashboard's client. prepare_sign_in stops when the file says otherwise.
REALM_NAME=openshell
CLIENT_ID=console

export DEV_GATEWAY_PORT="$GATEWAY_PORT" DEV_HEALTH_PORT="$HEALTH_PORT" \
  DEV_KEYCLOAK_PORT="$KEYCLOAK_PORT" DEV_REALM="$REALM_NAME"

ISSUER="http://127.0.0.1:${KEYCLOAK_PORT}/realms/${REALM_NAME}"
HOME_URL="http://localhost:${PROXY_PORT}"
HEALTH_URL="http://127.0.0.1:${HEALTH_PORT}/healthz"
# Where a sandbox calls the gateway back. The gateway runs each sandbox's
# supervisor on the Docker host's network, where the published port answers.
CALLBACK="http://127.0.0.1:${GATEWAY_PORT}"

say() { echo "dev-env: $*"; }
die() {
  echo "dev-env: $*" >&2
  exit 1
}

compose() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" --profile plain --profile sign-in "$@"
}

need() {
  local tool
  for tool in "$@"; do
    command -v "$tool" >/dev/null 2>&1 || die "$tool is not installed"
  done
}

pinned() {
  jq -er --arg key "$1" '.[$key]' "$PINS" || die "deploy/ci/gateway-pins.json has no $1"
}

# The Keycloak this repository's cluster manifest runs.
keycloak_image() {
  local image
  image="$(sed -n 's|^ *image: \(quay\.io/keycloak/keycloak:[^ ]*\)$|\1|p' "$ROOT/deploy/keycloak/dev/keycloak.yaml" | head -n 1)"
  [ -n "$image" ] || die "cannot read the Keycloak image from deploy/keycloak/dev/keycloak.yaml"
  echo "$image"
}

need_docker() {
  need docker jq openssl curl
  docker compose version >/dev/null 2>&1 || die "docker compose is not installed"
  OPENSHELL_GATEWAY_IMAGE="$(pinned gateway_image)"
  DEV_KEYCLOAK_IMAGE="$(keycloak_image)"
  export OPENSHELL_GATEWAY_IMAGE DEV_KEYCLOAK_IMAGE
  docker info >/dev/null 2>&1 || die "docker cannot reach a daemon at $(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || echo "${DOCKER_HOST:-its default socket}"). Start Docker, or choose the daemon with 'docker context use' or DOCKER_HOST."
}

# running prints the profile whose gateway is up: plain, sign-in, or nothing.
running() {
  if [ -n "$(compose ps -q --status running gateway-sign-in 2>/dev/null)" ]; then
    echo sign-in
  elif [ -n "$(compose ps -q --status running gateway 2>/dev/null)" ]; then
    echo plain
  fi
}

# port_free <port> <what listens there> <who may hold it, and what to do>.
# localhost, so that a listener on either loopback address counts: the UI dev
# server listens on one of them only.
port_free() {
  if (exec 3<>"/dev/tcp/localhost/$1") 2>/dev/null; then
    die "port $1 on this machine is in use, and $2 needs it. $3"
  fi
}

OTHER_STACK="If another checkout's stack holds it, run 'make dev-down' there."
OTHER_RUN="If 'make dev' or 'make dev-full' holds it, stop that one with Ctrl-C."

# ports [--sign-in]: what make dev starts on this machine, and with --sign-in
# what make dev-full does. Without this, the one that cannot listen stops and
# the others go on, in front of whatever holds its port. make dev also needs a
# gateway that is already there; make dev-full starts its own.
#
# ports --compat: the port the compat suite's gateway is published on
# (deploy/ci/docker-compose.e2e.yml), unless that gateway is the one holding
# it: then its health port answers. Docker does not always refuse a port that
# is taken, and the suite would then call whatever holds it.
ports() {
  if [ "${1:-}" = "--compat" ]; then
    curl -sf -o /dev/null --max-time 2 http://127.0.0.1:50052/healthz ||
      port_free 8080 "the compat suite's gateway" "$OTHER_RUN"
    return
  fi
  port_free "$UI_PORT" "the UI dev server" "$OTHER_RUN"
  port_free "$BFF_PORT" "the BFF" "$OTHER_RUN If the compat suite's gateway does, run 'make compat-down'."
  if [ "${1:-}" = "--sign-in" ]; then
    port_free "$PROXY_PORT" "the auth proxy" "$OTHER_RUN"
  elif [ -z "${OPENSHELL_GATEWAY_URL:-}" ] && ! (exec 3<>"/dev/tcp/127.0.0.1/$GATEWAY_PORT") 2>/dev/null; then
    # make dev would start, and every call of its API would answer 502.
    die "no gateway answers on 127.0.0.1:${GATEWAY_PORT}. Start it with 'make gateway', or set OPENSHELL_GATEWAY_URL to a gateway of your own."
  fi
}

# wait_for_gateway <service>: polls the health port, gives up as soon as the
# container has stopped, and shows its log when it does not answer.
wait_for_gateway() {
  local service="$1" waited=0
  say "waiting for the gateway..."
  while [ "$waited" -lt 120 ]; do
    if curl -sf -o /dev/null --max-time 2 "$HEALTH_URL"; then
      return 0
    fi
    if [ -z "$(compose ps -q --status running "$service" 2>/dev/null)" ]; then
      break
    fi
    sleep 2
    waited=$((waited + 2))
  done
  echo "dev-env: the gateway did not answer at $HEALTH_URL. Its log:" >&2
  compose logs --tail 40 "$service" >&2 || true
  mount_hint
  exit 1
}

mount_hint() {
  echo "dev-env: if a log says a path 'is not a regular file', or Keycloak has no realm, the Docker daemon cannot mount $STATE: set DEV_STATE_DIR to a directory it shares (with colima, under your home directory)." >&2
}

# prepare writes what the gateway reads: its config, rendered from the template
# the compat stack uses, and the keys it signs sandbox tokens with.
prepare() {
  local schema template supervisor sandbox
  schema="$(pinned config_schema)"
  supervisor="$(pinned supervisor_image)"
  sandbox="$(pinned sandbox_image)"
  template="$ROOT/deploy/ci/gateway.e2e.${schema}.toml.tmpl"
  [ -f "$template" ] || die "no gateway config template for schema $schema ($template)"

  mkdir -p "$STATE/jwt" "$STATE/keycloak"
  # The template's placeholders, and one line more: the label of this stack's
  # sandboxes (SANDBOX_LABEL above), as the first key of the Docker driver's table.
  sed -e "s|\${OPENSHELL_SUPERVISOR_IMAGE}|${supervisor}|g" \
    -e "s|\${COMPAT_SANDBOX_IMAGE}|${sandbox}|g" \
    -e "s|\${OPENSHELL_GRPC_ENDPOINT}|${CALLBACK}|g" \
    "$template" |
    awk -v label="$PROJECT" '{ print } /^\[openshell\.drivers\.docker\]$/ { print "sandbox_label     = \"" label "\"" }' \
      >"$STATE/gateway.toml"
  if grep -q '\${' "$STATE/gateway.toml"; then
    die "$template has a placeholder this script does not fill: $(grep -o '\${[A-Z_]*}' "$STATE/gateway.toml" | sort -u | tr '\n' ' ')"
  fi
  if [ "$(grep -c '^sandbox_label ' "$STATE/gateway.toml")" != 1 ]; then
    die "cannot put the sandbox label into $template: it needs one [openshell.drivers.docker] table and no sandbox_label of its own"
  fi

  if [ ! -f "$STATE/jwt/signing.pem" ]; then
    openssl genpkey -algorithm Ed25519 -out "$STATE/jwt/signing.pem" 2>/dev/null
    openssl pkey -in "$STATE/jwt/signing.pem" -pubout -out "$STATE/jwt/public.pem" 2>/dev/null
    openssl rand -hex 16 >"$STATE/jwt/kid"
  fi
  # The containers read these as another user, whatever the umask here.
  chmod -R a+rX "$STATE/gateway.toml" "$STATE/jwt" "$STATE/keycloak"
}

# secret <name> <bytes>: made once and kept, so that a restart does not sign
# anyone out or change a password.
secret() {
  [ -f "$STATE/$1" ] || (umask 077 && openssl rand -hex "$2" >"$STATE/$1")
  cat "$STATE/$1"
}

# prepare_sign_in writes the realm Keycloak imports at start: the repository's
# file, with a local callback, a client secret and two users added to it.
prepare_sign_in() {
  jq -e --arg realm "$REALM_NAME" '.realm == $realm' "$REALM" >/dev/null ||
    die "deploy/keycloak/openshell-realm.json is not the realm '$REALM_NAME' this script is written for"
  DEV_KEYCLOAK_ADMIN_PASSWORD="$(secret keycloak-admin-password 12)"
  export DEV_KEYCLOAK_ADMIN_PASSWORD
  secret cookie-secret 16 >/dev/null
  # The filter stops when the realm has lost the client or a group it changes.
  jq --arg client "$CLIENT_ID" --arg home "$HOME_URL" \
    --arg secret "$(secret client-secret 24)" --arg password "$(secret user-password 8)" \
    -f "$ROOT/deploy/dev/realm-local.jq" "$REALM" >"$STATE/keycloak/openshell-realm.json" ||
    die "could not write Keycloak's realm from deploy/keycloak/openshell-realm.json (jq's message is above)"
  chmod a+r "$STATE/keycloak/openshell-realm.json"
}

# What a person needs to sign in. make dev-full prints it twice: when the stack
# is up, and again when the auth proxy answers (see proxy).
sign_in_summary() {
  say "sign in at ${HOME_URL} (make dev-full serves it; not at port ${UI_PORT}, which the UI dev server prints) as admin1 (platform admin) or user1, password: $(cat "$STATE/user-password")"
  say "user1 is in no workspace until admin1 adds it under Members, by its principal subject 00000000-0000-4000-8000-000000000002"
  say "'scripts/dev-env.sh status' prints this again"
}

summary() {
  local mode service=gateway image release
  mode="$(running)"
  if [ -z "$mode" ]; then
    say "nothing is running. Start it with 'make gateway' or 'make dev-full'."
    return
  fi
  [ "$mode" = plain ] || service=gateway-sign-in
  # The release of the gateway that runs, which is the pinned one after `up`.
  image="$(docker inspect --format '{{.Config.Image}}' "$(compose ps -q "$service")")"
  release="${image#*/gateway:}"
  release="${release%%@*}"
  say "OpenShell gateway ${release} at 127.0.0.1:${GATEWAY_PORT} (plain HTTP, where make dev looks for it); Compose project ${PROJECT}"
  if [ "$mode" = sign-in ]; then
    say "Keycloak at http://127.0.0.1:${KEYCLOAK_PORT}, realm ${REALM_NAME}. Its admin UI: user admin, password in $STATE/keycloak-admin-password"
    sign_in_summary
  else
    say "without sign-in: make dev, then http://localhost:${UI_PORT}"
  fi
}

CANNOT_REMOVE="could not remove the stack's containers. Run 'make dev-down' and read what it prints."

start_stack() {
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" --profile "$1" up -d
}

# port_lost: after a failed start, whether Docker could not publish a port.
port_lost() {
  local ids
  ids="$(compose ps -aq 2>/dev/null)"
  [ -n "$ids" ] || return 1
  # shellcheck disable=SC2086
  docker inspect --format '{{.State.Error}}' $ids 2>/dev/null |
    grep -Eq 'address already in use|port is already allocated'
}

up() {
  local want=plain have service=gateway
  if [ "${1:-}" = "--sign-in" ]; then
    want=sign-in
    service=gateway-sign-in
  fi
  need_docker
  have="$(running)"

  if [ "$want" = plain ] && [ "$have" = sign-in ]; then
    say "already up, with sign-in. It serves make dev as well."
    summary
    return
  fi
  if [ "$want" = sign-in ] && [ "$have" = plain ]; then
    say "replacing the gateway with one that trusts Keycloak. Its data stays."
    compose down >/dev/null 2>&1 || die "$CANNOT_REMOVE"
    have=""
  elif [ -z "$have" ] && [ -n "$(compose ps -aq 2>/dev/null)" ]; then
    # Left by a start that did not finish, or by a gateway that stopped: they
    # hold the ports checked below. The data is in a volume and stays.
    compose down >/dev/null 2>&1 || die "$CANNOT_REMOVE"
  fi
  if [ -z "$have" ]; then
    port_free "$GATEWAY_PORT" "the gateway" "$OTHER_STACK"
    port_free "$HEALTH_PORT" "the gateway" "$OTHER_STACK"
    [ "$want" = plain ] || port_free "$KEYCLOAK_PORT" "Keycloak" "$OTHER_STACK"
  fi
  # Ctrl-C during the start is not a failed start: no logs, no hint.
  trap 'echo >&2; echo "dev-env: interrupted. What has started stays: run the command again, or make dev-down." >&2; exit 130' INT

  prepare
  if [ "$want" = sign-in ]; then
    prepare_sign_in
    say "starting Keycloak and the gateway..."
  fi
  if ! start_stack "$want"; then
    if ! port_lost; then
      echo "dev-env: the stack did not start. Its log:" >&2
      compose logs --tail 40 >&2 || true
      mount_hint
      exit 1
    fi
    # The published ports are in the range the system hands out for outgoing
    # connections, so one can be taken for a moment.
    say "a port the stack publishes was taken while it started. Trying once more..."
    compose down >/dev/null 2>&1 || die "$CANNOT_REMOVE"
    sleep 2
    start_stack "$want" || die "the stack did not start: Docker's message is above. Run the command again; if a port stays taken, find what holds it."
  fi
  wait_for_gateway "$service"
  summary
}

down() {
  need_docker
  compose down
}

reset() {
  need_docker
  local container volume
  compose down -v
  # A sandbox is a pair of containers and some volumes the gateway made outside
  # the Compose project. They carry this stack's label, running or not.
  for container in $(docker ps -aq --filter "label=${SANDBOX_LABEL}"); do
    docker rm -f "$container" >/dev/null
  done
  for volume in $(docker volume ls -q --filter "label=${SANDBOX_LABEL}"); do
    docker volume rm "$volume" >/dev/null
  done
  rm -rf "$STATE/gateway.toml" "$STATE/jwt" "$STATE/keycloak" "$STATE/client-secret" \
    "$STATE/cookie-secret" "$STATE/user-password" "$STATE/keycloak-admin-password" "$STATE/bin"
  rmdir "$STATE" 2>/dev/null || true
  say "removed the gateway's data, its sandboxes' containers and volumes, and the generated files in $STATE"
}

# logs follows each container with `docker logs`, so that one Ctrl-C ends it:
# `docker compose logs -f` takes two in some Compose versions.
logs() {
  local id ids
  ids="$(compose ps -q)"
  [ -n "$ids" ] || die "nothing is running. Start it with 'make gateway' or 'make dev-full'."
  trap 'kill $(jobs -p) 2>/dev/null || true' INT TERM
  for id in $ids; do
    docker logs -f --tail 100 "$id" 2>&1 &
  done
  wait || true
}

# bff_env [--sign-in] prints the BFF's settings, for make dev and make dev-full
# to eval.
#
# make dev: the local gateway, unless the caller names one of their own. The
# address is written out, so that the BFF does not warn that it is on its
# default.
#
# make dev-full: always the local gateway, the only one that trusts the local
# Keycloak, and what the BFF needs behind the auth proxy. The proxy logs out
# of Keycloak before clearing its cookie and returning to its sign-in page.
bff_env() {
  if [ "${1:-}" != "--sign-in" ]; then
    printf 'export OPENSHELL_GATEWAY_URL=%q\n' "${OPENSHELL_GATEWAY_URL:-127.0.0.1:${GATEWAY_PORT}}"
    return
  fi
  echo "export OPENSHELL_GATEWAY_URL=127.0.0.1:${GATEWAY_PORT}"
  echo "unset GATEWAY_CA_CERT GATEWAY_CLIENT_CERT GATEWAY_CLIENT_KEY"
  echo "export AUTH_DISABLED=false ADMIN_ROLE=openshell-admin USER_ROLE=openshell-user AUTH_USER_HEADER=x-forwarded-user"
  echo "export LOGOUT_URL='/oauth2/sign_out?rd=/oauth2/sign_in'"
}

# proxy_binary prints the path of oauth2-proxy at the version the Helm chart
# runs, and builds it first when it is not there. make dev-full builds it
# before anything starts, so that a download or a build that fails stops the
# command and not one of three jobs.
proxy_binary() {
  need go
  local version bin
  version="$(awk '$1 == "repository:" { ours = ($2 == "oauth2-proxy/oauth2-proxy") } ours && $1 == "tag:" { print $2; exit }' \
    "$ROOT/deploy/helm/console/values.yaml")"
  case "$version" in
    v[0-9]*) ;;
    *) die "cannot read the oauth2-proxy version from the chart's values.yaml" ;;
  esac
  bin="$STATE/bin/oauth2-proxy-$version"
  if [ ! -x "$bin" ]; then
    say "building oauth2-proxy $version (once; it downloads its modules)" >&2
    mkdir -p "$STATE/bin/build"
    GOBIN="$STATE/bin/build" go install "github.com/oauth2-proxy/oauth2-proxy/v7@${version}" >&2 ||
      die "could not build oauth2-proxy $version"
    mv "$STATE/bin/build/oauth2-proxy" "$bin"
    rmdir "$STATE/bin/build"
  fi
  echo "$bin"
}

# proxy runs oauth2-proxy with the flags the chart gives it
# (templates/deployment.yaml), changed for plain HTTP on this machine: no
# Secure cookie, an explicit callback, and the UI dev server as the upstream,
# so that the page still reloads on a change. It runs here and not in a
# container because the dev server listens on this machine's loopback address
# only.
proxy() {
  local bin waited=0
  bin="$(proxy_binary)"
  [ -f "$STATE/client-secret" ] || die "no client secret in $STATE: run 'scripts/dev-env.sh up --sign-in' first"
  OAUTH2_PROXY_CLIENT_SECRET="$(cat "$STATE/client-secret")"
  OAUTH2_PROXY_COOKIE_SECRET="$(cat "$STATE/cookie-secret")"
  export OAUTH2_PROXY_CLIENT_SECRET OAUTH2_PROXY_COOKIE_SECRET
  say "oauth2-proxy on ${HOME_URL}, in front of http://localhost:${UI_PORT}"
  # Where to sign in, said once more when the auth proxy, the BFF and the UI
  # dev server answer: by then the first time has scrolled away, under the
  # dev server's own address.
  (
    while [ "$waited" -lt 120 ]; do
      if curl -sf -o /dev/null --max-time 2 "http://127.0.0.1:${PROXY_PORT}/ping" &&
        curl -sf -o /dev/null --max-time 2 "http://localhost:${BFF_PORT}/api/v1/healthz" &&
        curl -s -o /dev/null --max-time 2 "http://localhost:${UI_PORT}/"; then
        echo
        say "ready."
        sign_in_summary
        break
      fi
      sleep 1
      waited=$((waited + 1))
    done
  ) &
  exec "$bin" \
    --provider=keycloak-oidc \
    --oidc-issuer-url="$ISSUER" \
    --client-id="$CLIENT_ID" \
    --upstream="http://localhost:${UI_PORT}/" \
    --http-address="127.0.0.1:${PROXY_PORT}" \
    --redirect-url="${HOME_URL}/oauth2/callback" \
    --backend-logout-url="${ISSUER}/protocol/openid-connect/logout?id_token_hint={id_token}" \
    --code-challenge-method=S256 \
    --allowed-role=openshell-api:openshell-admin \
    --allowed-role=openshell-api:openshell-user \
    --pass-access-token=true \
    --pass-authorization-header=false \
    --pass-basic-auth=false \
    --pass-user-headers=true \
    --skip-auth-strip-headers=true \
    --skip-jwt-bearer-tokens=false \
    --pass-host-header=true \
    --proxy-websockets=true \
    --email-domain='*' \
    --cookie-secure=false \
    --cookie-httponly=true \
    --cookie-name=_oauth2_proxy \
    --cookie-samesite=lax \
    --cookie-refresh=2m \
    --upstream-timeout=120s \
    --api-route='^/api/' \
    --skip-provider-button=true \
    --whitelist-domain="127.0.0.1:${KEYCLOAK_PORT}"
}

# compat_clean removes what the compat suite's gateway kept in the directory
# the Makefile gives it, so that each run starts from nothing, as in CI. That
# gateway writes as root. Where its files then belong to root on this machine
# too (a Docker daemon on Linux), a container removes them: one of the pinned
# sandbox image, which has a shell and is there once the suite has run. The
# gateway's own image has no shell.
compat_clean() {
  local dir="$ROOT/scripts/.state/compat" image
  [ -d "$dir" ] || return 0
  if rm -rf "$dir" 2>/dev/null; then
    return 0
  fi
  if image="$(jq -er .sandbox_image "$PINS" 2>/dev/null)" &&
    docker run --rm --pull=never --user 0 -v "$dir:/state" --entrypoint /bin/sh "$image" \
      -c 'rm -rf /state/* /state/.[!.]*' >/dev/null 2>&1 &&
    rmdir "$dir" 2>/dev/null; then
    return 0
  fi
  echo "dev-env: could not remove $dir, which the compat suite's gateway wrote as root. Remove it with: sudo rm -rf $dir" >&2
}

case "${1:-}" in
  up) up "${2:-}" ;;
  down) down ;;
  reset) reset ;;
  status)
    need_docker
    summary
    ;;
  logs)
    need_docker
    logs
    ;;
  ports) ports "${2:-}" ;;
  env) bff_env "${2:-}" ;;
  proxy)
    if [ "${2:-}" = "--build" ]; then
      proxy_binary >/dev/null
    else
      proxy
    fi
    ;;
  compat-clean) compat_clean ;;
  *)
    sed -n '2,11p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
    exit 2
    ;;
esac
