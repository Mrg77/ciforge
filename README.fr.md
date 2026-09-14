# ciforge

*Read this in [English](README.md).*

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Un agent IA qui durcit et optimise GitHub Actions — supply chain, permissions, minutes.**

Un runner de CI détient souvent plus de droits que l'ingénieur qui a écrit le
pipeline : il déploie, il lit des secrets, il pousse des images. Et il exécute du
code tiers sur un simple `uses:`. ciforge traite cette asymétrie pour ce qu'elle
est — une surface d'attaque — et la rapporte avec le correctif.

Écrit **from scratch sur l'API Anthropic Messages**, sans framework. Toute écriture
de workflow passe par une **policy-as-code** ; déclencher un pipeline est refusé.

> L'agent conseille. Il ne bloque jamais votre CI et ne la déclenche jamais : un gate
> doit rendre le même verdict sur le même diff, et un modèle ne peut pas le garantir.
> La sous-commande `audit` est déterministe et sans clé API — c'est elle qui a sa
> place dans un pipeline.

## Ce qu'il trouve

| Règle | Sévérité | Pourquoi ça compte |
|---|---|---|
| `action-not-pinned` | haute | Un tag est un pointeur. Qui contrôle le dépôt de l'action peut le déplacer vers un autre code, et votre pipeline l'exécute — sans aucun diff chez vous. |
| `pr-target-checkout` | haute | `pull_request_target` s'exécute avec vos secrets. Combiné au checkout de la PR, quiconque ouvre une pull request exécute du code qui y a accès. |
| `script-injection` | haute | Un titre de PR est une entrée utilisateur. Interpolé dans un `run:`, c'est du code. |
| `long-lived-credentials` | haute | Une clé cloud reste dans les secrets du dépôt jusqu'à ce qu'on la tourne, ce que personne ne fait. |
| `permissions-unset` | moyenne | Le jeton hérite du défaut du dépôt — souvent l'écriture. |
| `no-concurrency` | basse | Les runs périmés finissent quand même, et sont facturés. |

Les actions `actions/*` sont rapportées en **basse** sévérité : un risque moindre,
pas nul. Noyer un vrai risque sous cinquante avertissements revient à le cacher.

## Installer

```sh
# Homebrew
brew install mrg77/tap/ciforge

# ou l'installeur (Linux, macOS)
curl -fsSL https://raw.githubusercontent.com/Mrg77/ciforge/main/install.sh | sh

# ou avec Go
go install github.com/Mrg77/ciforge@latest
```

## Utiliser

Déterministe, sans clé API — c'est cette commande qui va dans la CI :

```sh
ciforge audit                  # sort en 1 sur un finding haut
ciforge pin                    # le SHA de chaque action épinglée à un tag
```

Avec l'agent :

```sh
export ANTHROPIC_API_KEY=...
ciforge "audite mes workflows et épingle toutes les actions tierces"
ciforge "migre le workflow de déploiement des clés AWS vers OIDC"
```

Le modèle par défaut est `claude-sonnet-5` — la boucle d'agent fait beaucoup
d'allers-retours d'outils et paie chacun, donc c'est la capacité par token qui
compte. `CIFORGE_MODEL=claude-opus-5` sur `audit . --explain` ou `fix` achète un
meilleur jugement pour quelques centimes, puisque ces commandes ne font qu'un
appel et produisent du code qu'un humain va appliquer.

## La garde

Un fichier de workflow est le chemin le plus court vers la production. Un agent qui
peut en réécrire un peut s'octroyer des permissions, ajouter une étape qui exfiltre
un secret, ou livrer du code — et rien de tout ça n'a l'air alarmant dans un diff.

| Action | Workflow de déploiement | Autre workflow | Ailleurs |
|---|---|---|---|
| écrire / éditer | **refusé** | confirmation | confirmation |
| déclencher | **refusé** | **refusé** | **refusé** |
| auditer, épingler, coût | autorisé | autorisé | autorisé |

Le contexte est lu **passivement** depuis le chemin visé : rien n'est récupéré, rien
n'est déclenché. Une cible non classifiable **échoue fermé**. Sans terminal (CI,
pipe), une confirmation devient un refus.

## OIDC, correctement

Quand ciforge propose de remplacer des clés longue durée, il insiste sur ce qu'on
oublie : la politique de confiance du rôle doit être restreinte au dépôt **et** à la
branche ou l'environnement. Un sujet avec joker donne le rôle à tous les dépôts de
l'organisation, ce qui annule l'intérêt de la migration.

## En serveur MCP

`ciforge mcp [racine]` sert les contrôles déterministes via le Model Context
Protocol : un assistant les appelle directement au lieu de lancer un shell et de
parser du texte. `racine` est le dossier pris par défaut — omets-le pour le
dossier courant.

Installe d'abord le binaire (voir plus haut) : le serveur *est* ce binaire, il n'y
a rien d'autre à télécharger, et surtout pas le dépôt. Puis, depuis le dépôt à
analyser :

```sh
claude mcp add -s project ciforge -- ciforge mcp
```

ce qui écrit `.mcp.json` à la racine du dépôt :

```json
{
  "mcpServers": {
    "ciforge": { "command": "ciforge", "args": ["mcp"] }
  }
}
```

Commite ce fichier et toute l'équipe a les mêmes outils — à condition d'avoir
installé ciforge aussi. Claude Code demande une approbation la première fois qu'il
voit un `.mcp.json` qu'on ne lui a pas encore montré : un dépôt cloné ne peut donc
pas lancer un processus dans ton dos. Vérifie avec `claude mcp list`, ou `/mcp`
en session.

Exposé : `workflow_audit, workflow_pin` — lecture seule, gratuit, sans effet de bord même appelé
en boucle.

**Non exposé : `fix`, l'agent, tout ce qui écrit.** Un serveur MCP est piloté par un modèle,
le plus souvent sans qu'un humain approuve chaque appel. Exposer un outil qui
écrit reviendrait à donner à un agent la capacité que les gardes existent
justement pour retenir, et par un canal où la politique ne s'exécute jamais. Le
serveur n'offre que ce qui ne change rien.

## Honnêteté

- Si `actionlint` manque ou si l'API GitHub limite, ciforge dit que le contrôle
  **n'a pas eu lieu**. Le silence ne doit jamais se lire comme un succès.
- Les chiffres de coût sont des **estimations**, annoncées comme telles.
- Toute optimisation ne vaut pas sa maintenance, et l'outil le dit.

## Licence

MIT.
