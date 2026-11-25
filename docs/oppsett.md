# Oppsett

Engangsjobben før tjenesten kjører i klusteret. Manifestene ligger i
Homelab under `kubernetes/apps/hyttevarsel/`.

## 1. Gmail-kontoen for varslene

1. Lag en egen Gmail-konto, for eksempel `hyttevarsel.<noe>@gmail.com`. Den
   blir varseladressen Kristine sender til og får varsler fra.
2. Slå på totrinnsbekreftelse og lag et **app-passord**
   (myaccount.google.com → Sikkerhet → App-passord). Samme passord brukes
   for SMTP og IMAP.
3. IMAP er på som standard i nye kontoer. Sjekk under Innstillinger →
   Videresending og POP/IMAP.

Tjenesten merker e-post som lest når den er behandlet, også e-post den
ignorerer. Gmails bekreftelseskode for videresending (bruksanvisningen,
«Slutt å følge med av seg selv») ligger derfor som lest i innboksen.

## 2. Hemmelighetene (på mac-1, i Homelab-klonen)

E-postoppsettet, kryptert til eieren og klusteret (`.sops.yaml`):

```sh
umask 077
cat > kubernetes/apps/hyttevarsel/smtp.sops.yaml <<'YAML'
apiVersion: isindir.github.com/v1alpha3
kind: SopsSecret
metadata:
  name: hyttevarsel-smtp
  namespace: hyttevarsel
  annotations:
    argocd.argoproj.io/sync-options: SkipDryRunOnMissingResource=true
spec:
  enforceOwnership: true
  secretTemplates:
    - name: hyttevarsel-smtp
      stringData:
        SMTP_USER: <varseladressen>
        SMTP_PASSWORD: <app-passordet>
        MAIL_TO: <Kristines adresse>
        BASE_URL: https://hyttevarsel.<tailnettet>.ts.net
YAML
sops -e -i kubernetes/apps/hyttevarsel/smtp.sops.yaml
```

Varselagenten, med skriptet Homelab har for dette:

```sh
scripts/sops-secret.sh kubernetes/apps/hyttevarsel/varsel-agent.sops.yaml \
  hyttevarsel varsel-agent \
  url=/run/secrets/varsel-agent-url token=/run/secrets/varsel-agent-token
```

## 3. Første test

1. Send en hyttelenke fra Kristines adresse til varseladressen. Innen et
   minutt skal bekreftelsen komme.
2. Følg med på en hytte som er ledig nå, så kommer et varsel med en gang.
   Svar «stopp» på det.
3. Loggen: `kubectl -n hyttevarsel logs deploy/hyttevarsel`.

## 4. Nettsiden (valgfri)

Oversikten er på `https://hyttevarsel.<tailnettet>.ts.net`. Kristine
trenger den ikke. Vil hun se den, deles maskinen «hyttevarsel» med henne i
Tailscale-adminen (Share), så får hun bare den.
