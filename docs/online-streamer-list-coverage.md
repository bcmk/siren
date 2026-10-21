# Online streamer list coverage

Coverage is how many streamers the site's API lists out of how many are really online,
and the share of those listed that get details, such as an image and a viewer count.
Changing how a checker queries a site can change either.

## How to measure a change

When changing a checker's online list query,
compare the old version with the new one in repeated tries:

- Run 20 tries, 40 seconds apart, or back to back when a try takes longer
- In each try, run the checker's `QueryOnlineStreamers` in both versions concurrently
- Log both versions' online counts, and diff their online lists
- If the checker can list a streamer without details, as Stripchat does,
  do the same for the streamers with details
- Subtract the old version's counts from the new version's in each try,
  then take the median of these differences over all tries

The online count drifts over the run as streamers come and go.
Both versions of a try run together, so the drift cancels out of their difference.
Comparing each version's own median instead would bury the effect in the drift.

If the result may depend on where the requests come from,
measure from more than one location, through a proxy if needed.

## Results

### Stripchat `applyGeobans=0`, 2026-10-21

The new version added `applyGeobans=0` to `/online` and `/models`.
We compared it with the old version, which sent neither.
20 tries each from the Netherlands and from Colombia.
All percentages are of the old version's median `/online` count.
Differences are new minus old, with the number of streamers in parentheses.
Shares divide each version's median `/models` count by the old version's median `/online` count.
Two shares in a cell are the old version's, then the new one's.

|                                        | Netherlands      | Colombia          |
| -------------------------------------- | ---------------- | ----------------- |
| `/online` count, median of differences | −0.15% (−11.5)   | +0.20% (+18.5)    |
| `/online` count, tries out of 20       | 8 more, 11 fewer | 12 more, 6 fewer  |
| `/models` count, median of differences | +0.25% (+19.5)   | +25.85% (+2418.5) |
| `/models` count, tries out of 20       | 20 more, 0 fewer | 20 more, 0 fewer  |
| `/models` share of `/online`           | 99.22%, 99.49%   | 73.16%, 99.10%    |

Without `applyGeobans=0`, `/models` leaves out streamers geobanned where the request comes from.
`/online` lists them either way, so the bot adds the parameter to `/models` only.
