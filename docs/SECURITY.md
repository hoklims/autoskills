# Modèle de menace d’AutoSkills

État initial au 30 août 2026. Ce document décrit les frontières à protéger; il ne prétend pas que toutes les mesures sont déjà mises en œuvre.

## Actifs

- code et documents privés présents dans les transcripts;
- secrets, tokens, clés, URLs internes et données personnelles;
- instructions persistantes consommées par Claude Code, Codex et Cursor;
- registry, base SQLite, journal et sauvegardes;
- fichiers utilisateur situés hors des sections gérées;
- budget et endpoint LLM configurés par l’utilisateur.

## Adversaires et défaillances

- transcript contenant une prompt injection, volontaire ou issue d’un log/code externe;
- secret présent dans une transcript, un fichier de règles ou une configuration;
- réponse provider malformée, hostile ou simplement erronée;
- page web locale malveillante qui tente une requête vers l’API de review;
- base corrompue, panne entre écriture filesystem et transaction SQLite;
- substitution de chemin, junction, symlink ou casing ambigu;
- migration ou rollback incomplet;
- commande présente dans une transcript et interprétée comme instruction.

## Frontières de confiance

| Entrée ou sortie | Niveau | Règle |
|---|---|---|
| Transcripts et contenu de code/log | Non fiable | Parser comme données. Ne jamais exécuter. Neutraliser les marqueurs de contrôle. |
| Contexte AGENTS/CLAUDE/Cursor | Sensible | Redacter avant tout egress; borner le volume et la portée. |
| Provider LLM et sa réponse | Non fiable | Utiliser TLS pour le distant; valider schéma, taille, provenance et destinations. |
| Review UI locale | Autorité utilisateur | Loopback strict par défaut; contrôler Host/Origin, taille et délais; capability explicite si exposition. |
| Registry et store | Autorité locale | Transactions, version de schéma, integrity check, sauvegarde et journal. |
| Writers | Mutation sensible | Racines canoniques autorisées, plan complet, temp+fsync+rename, diff et rollback. |

## Invariants

1. Aucune donnée ne quitte la machine avant redaction et policy outbound.
2. Une transcript ne peut pas modifier le control plane par son contenu.
3. Une réponse LLM reste une proposition jusqu’à validation et review autorisée.
4. Un bloc shell proposé ne devient jamais un fichier exécutable sans autorisation dédiée, distincte de l’acceptation du texte.
5. Une mutation ne touche que les racines et sections explicitement gérées.
6. Le store, le journal et le filesystem convergent après succès comme après panne.
7. Le rollback restaure l’état précédent exact ou échoue sans détruire davantage.
8. Le daemon n’élargit pas l’autorité du CLI.

## Mesures requises

### Avant egress

- redacter clés API, bearer tokens, private keys, connection strings, credentials cloud, `.env`, emails et URLs internes selon la policy;
- limiter taille, nombre d’éléments et champs envoyés;
- refuser un endpoint distant sans TLS sauf opt-in local explicite;
- journaliser les compteurs, jamais les secrets ni le payload brut.

### Après réponse provider

- décoder un schéma fermé avec limites de taille et enums;
- vérifier que chaque preuve appartient aux données autorisées;
- recalculer sensibilité, confiance et destination côté local;
- supprimer l’auto-accept actuel; une exception future exigera une policy locale déterministe et restera interdite au contenu exécutable;
- présenter le plan complet des fichiers et le diff avant mutation.

### Pendant une mutation

- résoudre les chemins canoniques et vérifier leur confinement;
- préparer toutes les écritures dans le même filesystem;
- enregistrer manifeste, preimages et checksums dans une saga `prepared → applying → committed`;
- remplacer chaque fichier atomiquement et rendre chaque étape rejouable;
- réconcilier au démarrage toute saga incomplète, puis terminer ou restaurer;
- n’avancer les checkpoints qu’après la persistance durable des objets qu’ils couvrent.

## Tests adversariaux minimaux

- clés OpenAI/Anthropic/GitHub, tokens AWS/GCP/Azure et private keys;
- URL avec utilisateur/mot de passe, DSN SQL et connection strings;
- `.env` mêlé à du texte normal;
- marqueurs `AGENTS.md`, `CLAUDE.md`, Markdown et instructions d’outil injectées;
- blocs shell et commandes destructrices dans une transcript;
- symlink/junction substitué entre plan et commit;
- panne après chaque étape d’une opération multi-fichiers;
- corruption JSON/SQLite et migration interrompue;
- requête cross-origin vers la review API;
- transcript tronquée, remplacée ou réécrite après checkpoint.

## Fermé par HOK-539

- tout payload provider est construit par `internal/outbound`: redaction, neutralisation des marqueurs et bornes de taille sur chaque valeur dynamique, puis redaction de l’ensemble assemblé. `llm.Client.Chat` n’accepte plus qu’un `outbound.Payload`, dont les champs sont non exportés;
- les chemins transcript, contexte de dépôt (`AGENTS.md`, `CLAUDE.md`, `.cursor/rules`), métadonnées de session et blocs gérés du gardener passent tous par cette préparation unique;
- l’endpoint est validé avant la construction de toute requête portant la clé: HTTPS pour le distant, HTTP uniquement sur loopback, ni userinfo ni schéma inattendu; les redirects sont refusés pour éviter de rejouer corps et credentials vers une autre autorité;
- l’auto-accept est supprimé. `auto_accept_threshold` reste parsé pour compatibilité, n’écrit jamais, et déclenche un avertissement explicite au scan;
- réponse provider décodée comme un unique objet JSON sans champs inconnus, puis validée contre un schéma fermé (enums vérifiés et non coercés, champs requis, bornes de taille, confiance dans `[0,1]`, preuves vérifiées verbatim contre le texte réellement envoyé); scope, placement et destination sont déterminés localement et les valeurs provider correspondantes sont ignorées;
- l’artefact est décrit par un plan déterministe (`writer.BuildPlan`) validé avant toute écriture — destination confinée sous sa racine canonique malgré symlinks/junctions, marqueurs gérés interdits dans le contenu, et un placement `path_scoped` sans glob ne peut pas devenir global;
- plus aucune extraction de fence shell: `run.sh` n’est plus généré et aucun artefact écrit n’est exécutable.

## Limites restantes

- les writers et le store ne forment pas une transaction de bout en bout (HOK-540);
- la review API peut être liée à une interface non loopback (HOK-541);
- la redaction reste heuristique: elle couvre les formes listées ci-dessus, pas tout secret concevable;
- les clés peuvent être stockées en clair dans la configuration;
- la matrice CI ne qualifie pas Windows ni macOS.

Les issues HOK-540, HOK-541 et HOK-547 portent les fermetures restantes. Jusqu’à leur validation, AutoSkills reste un prototype à utiliser uniquement sur des données et dépôts dont l’utilisateur accepte le risque.
