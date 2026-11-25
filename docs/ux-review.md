# UX- og designreview, 25. nov. 2026

Gjennomgang av hele løpet slik Kristine møter det: oppskriftssiden, hver
e-post tjenesten sender, svarene hun kan gi, og lenkene til DNT.

## Konklusjon

Kjernen er god: del en lenke, få en e-post, trykk «Bestill nå». Siden er
ryddig. Svakhetene ligger rundt kjernen: e-postene har ikke fått samme
omsorg som siden, to svar virker ikke som teksten lover, og det
vanskeligste steget (snarveien på iPhone) er lagt på henne. Ingenting er
ennå prøvd med ekte e-post; loggen viser at ingen hytte er lagt inn.

## Hva som er sjekket

- Alle e-posttypene er rendret og sett på i mobilbredde.
- Siden er sett i nettleser på mobil og PC.
- Svar-tolkningen er prøvd med fem typiske svar.
- Bestillingslenken er åpnet i nettleser: DNT-siden viser «2. des. – 3. des.»
  og «2 gjester», altså fylles dato og antall inn. Siden viste 12/12 ledige
  senger, samme tall som tjenesten regner seg fram til.

Ikke sjekket: sending til Hotmail, snarveien på en ekte iPhone,
bokmerket på PC, og om lenken hun deler fra mobilen faktisk inneholder
datoene hun valgte.

## A. Rettes før hun tar den i bruk

1. **Første e-post kan havne i søppelpost.** Avsenderen er en helt ny
   Gmail-konto med automatisk innhold, og mottakeren er Hotmail. Havner
   varselet i søppelpost, er hytta borte. *Anbefaling:* send en
   velkomst-e-post først. Hun finner den, markerer avsenderen som trygg og
   lagrer kontakten derfra. Det er samtidig den første ekte testen.
2. **«Stopp» virker ikke på ukesoppsummeringen.** Svarer hun «stopp» der,
   får hun oversikten en gang til. «stopp [#3]», som e-posten selv ber
   henne skrive, virker heller ikke, fordi nummeret bare leses fra emnet.
   *Anbefaling:* les nummeret fra teksten også. Følger hun bare med på én
   hytte, stopp den. Ellers spør hvilken.
3. **Antall personer telles feil med ungdom og barn.** Bare voksne leses
   fra lenken. To voksne og to barn blir «2 personer», og varselet kommer
   når det er to ledige senger. *Anbefaling:* summer alle kategoriene i
   lenken.
4. **Avsendernavnet mangler.** Fra-feltet er bare adressen, så innboksen
   viser «hyttevarsel.chaerem». *Anbefaling:* «Hyttevarsel» som navn.
5. **Ingenting sørger for at hun merker varselet.** E-post er en treg
   kanal, og plassene går fort. Siden har ikke lenger steget om å slå på
   varsler. *Anbefaling:* legg til «Slå på varsler for Hyttevarsel» i Kom
   i gang (favoritt i Outlook, VIP i Mail). Vurder SMS de siste ti dagene
   før ankomst hvis e-post viser seg for tregt.
6. **Videresending av DNT-e-post kan gi rot.** Regelen på siden sender
   all e-post fra dnt.no videre, også nyhetsbrev. Slik koden er nå, får
   hun et «fant ingen hyttelenke»-svar for hver av dem, og et nyhetsbrev
   med hyttelenker kan bli tolket som en bestilling av varsel.
   Gjenkjenningen av ordrebekreftelser er dessuten laget uten en ekte
   bekreftelse å se på. *Anbefaling:* ta delen ut av siden til den er
   prøvd med en ekte bekreftelse, og ignorer videresendt e-post som ikke
   er en bekreftelse.

## B. Gjør det enklere

7. **Snarveien på iPhone er det vanskeligste steget.** Fire steg i en app
   hun kanskje aldri har åpnet. *Anbefaling:* lag snarveien én gang og del
   den som iCloud-lenke. Da blir det ett trykk på «Legg til snarvei».
   Lenken må lages på en iPhone, så det er Christophers steg. Avklar også
   hvilken telefon og e-postapp hun bruker, og vis bare den oppskriften.
8. **Siden snakker til alle, men tjenesten virker bare for henne.** Andre
   som følger oppskriften får aldri svar. *Anbefaling:* skriv siden til
   henne, ta varseladressen ut av den, og be søkemotorer la den være.
   Adressen får hun i velkomst-e-posten.
9. **Interne numre i emnene.** «Ledig: Skåla lør 12. juli [#4]» og
   «stopp [#3]» er tjenestens bokføring, ikke hennes. *Anbefaling:* bruk
   e-posttråden til å vite hvilken hytte et svar gjelder, og fjern
   numrene. I oversikten får hver linje sin egen «Stopp»-lenke.
10. **«Jeg har booket» er en e-postlenke.** Den åpner telefonens
    standardapp, som kan være en annen enn den hun bruker, og gjør ofte
    ingenting i Hotmail på nett. *Anbefaling:* la «svar stopp» være
    hovedveien i teksten, og behold lenkene som snarvei.
11. **To e-poster når hytta allerede er ledig.** Bekreftelse og varsel
    kommer rett etter hverandre. *Anbefaling:* én e-post: «Det er ledig
    nå», med knappen.
12. **Treg første bekreftelse.** Innboksen leses hvert minutt, og hver
    dato sjekkes med tre sekunders mellomrom. En måned med fleksible
    datoer gir halvannet minutt ekstra. *Anbefaling:* lytt på innboksen i
    stedet for å spørre hvert minutt, og send bekreftelsen før alle
    datoene er sjekket.
13. **Bokmerket på PC er uvant.** Å dra en knapp til bokmerkelinja er
    ikke noe folk flest har gjort. *Anbefaling:* si først «kopier
    adressen og lim den inn i en e-post», og la bokmerket være tillegget.

## C. Design og tekst

14. **E-postene ser ikke ut som siden.** Siden viser et eksempel med
    grønn knapp. E-posten hun får har rød knapp, kremfarget bakgrunn og
    en annen skrift. *Anbefaling:* samme farger og typografi, og la
    eksempelet på siden være et bilde av den ekte e-posten. Fjern
    «1843 moh.» fra eksempelet; det står ikke i e-posten.
15. **Tonen i e-postene.** «Booket! God tur», «God tur!» og «Det er
    faktisk ledig allerede nå» er muntrere enn siden. *Anbefaling:*
    samme nøkterne tone overalt.
16. **Ukesoppsummeringen er en tekstvegg.** «Skåla: Skåla fra lørdag …»
    gjentar navnet, og hver linje er en hel setning. *Anbefaling:* én
    linje per hytte med navn, dato og status.
17. **Samme bunntekst på alle e-poster.** «Svar stopp på denne e-posten»
    står også der det ikke virker. *Anbefaling:* bunntekst per e-posttype.
18. **Hjelpesvaret er fire lange avsnitt.** *Anbefaling:* to linjer og
    lenke til siden.
19. **Rutevarselet har tre like knapper.** *Anbefaling:* nummerer dem
    («1 av 3») og si at alle tre må bestilles for at turen skal gå opp.
20. **«Mellom 10. og 14. juli» gir ankomst 10.–13.** Det er riktig, men
    kan overraske. *Anbefaling:* skriv «siste natt 13.–14. juli» i
    bekreftelsen.

## Anbefalt rekkefølge

Først 1–6, som er feil og risiko. Deretter 8–11 og 14–18, som bare er
kode og tekst. Punkt 7 krever en iPhone og et svar på hvilken telefon
hun har. Punkt 12 og 13 kan vente til hun har brukt tjenesten en uke.
