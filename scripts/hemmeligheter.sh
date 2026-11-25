#!/usr/bin/env bash
# Legger hyttevarselets to hemmeligheter inn i Homelab som SopsSecrets og
# åpner PR. Kjøres av eieren på mac-1 (nix, gh og SSH til linux-4), etter at
# Gmail-kontoen og app-passordet finnes (docs/oppsett.md):
#
#   curl -fsSL https://raw.githubusercontent.com/CHaerem/hyttevarsel/main/scripts/hemmeligheter.sh | bash
#
# Spør etter varseladressen, app-passordet, Kristines adresse og
# tailnet-navnet, og prøver innloggingen mot Gmail før noe pushes.
# Varselagentens URL og token hentes fra sops-nix på linux-4, som i
# Homelabs scripts/sops-secret.sh. Ingen verdi står i argv eller på disk i
# klartekst: de går gjennom miljøvariabler og pipes rett inn i sops.
set -euo pipefail

tty=/dev/tty
read -rp "Varseladressen (Gmail): " SMTP_USER <"$tty"
read -rsp "App-passordet (16 tegn, mellomrom gjør ikke noe): " SMTP_PASSWORD <"$tty"; echo
read -rp "Kristines e-postadresse: " MAIL_TO <"$tty"
# Bare for lenken til oversikten nederst i e-postene. Kristine er ikke på
# tailnettet, så den kan stå tom.
read -rp "Tailnet-navnet for lenken til oversikten (valgfri, Enter for å hoppe over): " TAILNET <"$tty"
SMTP_PASSWORD=${SMTP_PASSWORD// /}
export SMTP_USER SMTP_PASSWORD MAIL_TO
export BASE_URL=""
[ -n "$TAILNET" ] && BASE_URL="https://hyttevarsel.$TAILNET.ts.net"

echo "Prøver innloggingen mot Gmail (IMAP)..."
# -K leser brukernavn og passord fra en pipe, ikke fra argv.
if ! printf 'user = "%s:%s"\n' "$SMTP_USER" "$SMTP_PASSWORD" \
  | curl -sS --fail -K - "imaps://imap.gmail.com/INBOX" -X "EXAMINE INBOX" >/dev/null; then
  echo "Innloggingen feilet. Sjekk adressen, app-passordet og at IMAP er på. Ingenting er endret."
  exit 1
fi
echo "Innloggingen virker."

dir=$(umask 077; mktemp -d)
trap 'rm -rf "$dir"' EXIT
gh repo clone CHaerem/Homelab "$dir/repo" -- -q --depth 1
cd "$dir/repo"
gren="hyttevarsel-hemmeligheter-$(date +%Y%m%d-%H%M)"
git checkout -q -b "$gren"

sopssecret() { # <fil> <navn>; stringData som JSON på stdin
  # shellcheck disable=SC2094  # sops leser stdin; --filename-override styrer bare regelen
  DATA=$(cat) NAVN="$2" nix shell nixpkgs#jq -c jq -n '{
      apiVersion: "isindir.github.com/v1alpha3",
      kind: "SopsSecret",
      metadata: {
        name: env.NAVN,
        namespace: "hyttevarsel",
        annotations: {"argocd.argoproj.io/sync-options": "SkipDryRunOnMissingResource=true"}
      },
      spec: {
        enforceOwnership: true,
        secretTemplates: [{name: env.NAVN, stringData: (env.DATA|fromjson)}]
      }
    }' \
    | nix shell nixpkgs#sops -c sops --encrypt --input-type yaml --output-type yaml \
        --filename-override "$1" /dev/stdin >"$1"
  grep -q 'ENC\[' "$1" || { echo "Krypteringen av $1 feilet, ingenting pushet."; exit 1; }
}

nix shell nixpkgs#jq -c jq -n '{SMTP_USER: env.SMTP_USER, SMTP_PASSWORD: env.SMTP_PASSWORD, MAIL_TO: env.MAIL_TO} + (if env.BASE_URL != "" then {BASE_URL: env.BASE_URL} else {} end)' \
  | sopssecret kubernetes/apps/hyttevarsel/smtp.sops.yaml hyttevarsel-smtp
unset SMTP_PASSWORD

URL=$(ssh -n -o BatchMode=yes chaerem@linux-4 "sudo cat /run/secrets/varsel-agent-url")
TOKEN=$(ssh -n -o BatchMode=yes chaerem@linux-4 "sudo cat /run/secrets/varsel-agent-token")
[ -n "$URL" ] && [ -n "$TOKEN" ] || { echo "Fant ikke varselagenten på linux-4, ingenting pushet."; exit 1; }
URL="$URL" TOKEN="$TOKEN" nix shell nixpkgs#jq -c jq -n '{url: env.URL, token: env.TOKEN}' \
  | sopssecret kubernetes/apps/hyttevarsel/varsel-agent.sops.yaml varsel-agent
unset URL TOKEN

git add kubernetes/apps/hyttevarsel/*.sops.yaml
git -c user.name="Christopher Hærem" -c user.email="chris.haerem@gmail.com" commit -q -m "hyttevarsel: e-posten og varselagenten som SopsSecrets

Kryptert til eieren og klusteret (.sops.yaml), laget med
scripts/hemmeligheter.sh i CHaerem/hyttevarsel. Gmail-innloggingen er
prøvd før push."
git push -q -u origin "$gren"
gh pr create -R CHaerem/Homelab --base main --head "$gren" \
  --title "hyttevarsel: e-posten og varselagenten som SopsSecrets" \
  --body "Hemmelighetene \`hyttevarsel/hyttevarsel-smtp\` (Gmail-kontoen for varslene, Kristines adresse, oversiktens adresse) og \`hyttevarsel/varsel-agent\` (fra linux-4), laget med \`scripts/hemmeligheter.sh\` i CHaerem/hyttevarsel. Gmail-innloggingen er prøvd før push. Kryptert til eieren og klusteret. Rører \`kubernetes/**/*.sops.yaml\`, så den merges av eieren. Når den er merget, starter poden i \`hyttevarsel\`."
