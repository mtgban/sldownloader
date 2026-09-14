# Foil/Etched/Token detection is a raw-line substring check

**Category**: Correctness
**Files**: [main.go](../main.go) — `processLine`

## Problem

```go
card.Foil = strings.Contains(strings.ToLower(line), "foil")
card.Etched = strings.Contains(strings.ToLower(line), "etched")
card.Token = strings.Contains(strings.ToLower(line), "token")
```

These three flags are derived from a case-insensitive **substring check
against the entire raw (uncleaned) line**, not from whatever `cleanLine`
determined the actual variant tags to be. This means a card whose real
name simply *contains* one of these words anywhere gets misclassified,
independent of whether that occurrence was ever meant as a variant-tag
marker.

This is not hypothetical for "Foil" and "Etched" specifically: `cleanLine`
already has to special-case exactly this situation for the *name-cleaning*
step (`keepFoil`/`keepEtched` in §8.3 of SPECIFICATIONS.md, protecting real
cards like "Lavinia, Foil to Conspiracy" and the seven "Etched ___" cards
from having their name corrupted) — but that protection only stops the
*name text* from being stripped. It does nothing for `processLine`'s
separate `Foil`/`Etched`/`Token` boolean detection, which runs against the
raw line independently and has no equivalent exclusion list.

Concretely: a line reading `"1x Lavinia, Foil to Conspiracy"` (no actual
foil-variant tag present, and correctly *not* one — nothing in this
product listing said this particular copy was a foil-finish printing) would
still set `card.Foil = true`, purely because the literal substring "foil"
appears in the card's own name.

## Impact

Any product line for a card whose own name contains "Foil," "Etched," or
"Token" would get a spurious `[foil]`/`[etched]`/`[token]` suffix tag in
the output file (§6.1 in SPECIFICATIONS.md), even on a printing that isn't
actually that finish. Whether this has manifested in an actual output file
depends on whether "Lavinia, Foil to Conspiracy" (or the "Etched ___"
cards) has ever appeared in a real Secret Lair Drop product processed by
this tool — not confirmed either way, but the code path is real and
reachable, not theoretical.

## Suggested approach

- The most direct fix is to derive these three flags from the **same**
  tag-detection logic `cleanLine` already performs, rather than
  re-deriving them independently against the raw line. Concretely: have
  `cleanLine` (or a sibling function sharing its `nameTagRegexps`
  machinery) report which of the "Foil"/"Etched"/"Token"/"Tokens" tags it
  actually *stripped* as a real tag occurrence (as opposed to a occurrence
  that was protected because it's part of the actual card name, per the
  existing `keepFoil`/`keepEtched` guards), and have `processLine` use
  that report instead of its own independent substring check.
- This directly reuses the exclusion knowledge that already exists for
  name-cleaning, rather than maintaining two separate, divergent notions
  of "does this line mention a foil/etched/token tag."
