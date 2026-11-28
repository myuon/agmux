import { useEffect, useState } from "react";
import { api, type ChoiceCard } from "../api/client";
import type { Session } from "../types/session";
import { useExperimental } from "./useExperimental";
import { visibleInterval } from "./usePageVisible";

/**
 * Returns lines with synthetic `choice_card` entries inserted at each card's
 * stream offset. Offsets are absolute; historyStart is the absolute index of
 * lines[0]. Cards older than the loaded window and dismissed cards are skipped.
 */
export function spliceChoiceCards(lines: unknown[], historyStart: number, cards: ChoiceCard[]): unknown[] {
  const visible = cards
    .filter((c) => c.status !== "dismissed" && c.streamOffset >= historyStart)
    .map((c) => ({ card: c, at: Math.min(c.streamOffset - historyStart, lines.length) }));
  if (visible.length === 0) return lines;
  // Stable sort by position (cards arrive in creation order).
  visible.sort((a, b) => a.at - b.at);
  const out: unknown[] = [];
  let next = 0;
  for (const { card, at } of visible) {
    while (next < at) out.push(lines[next++]);
    out.push({ type: "system", subtype: "choice_card", card_id: card.id });
  }
  while (next < lines.length) out.push(lines[next++]);
  return out;
}

const NONE = new Set<string>();

/** Session ids that currently have an open choice card (empty while the feature is off). */
export function useOpenChoiceSessions(sessions: Session[]): Set<string> {
  const enabled = useExperimental("choice_cards");
  const [ids, setIds] = useState<Set<string>>(new Set());
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    const load = () =>
      api
        .listOpenChoiceCards()
        .then((r) => {
          if (!cancelled) setIds(new Set(r.cards.map((c) => c.sessionId)));
        })
        .catch(() => {});
    load();
    const stopPoll = visibleInterval(load, 15_000);
    return () => {
      cancelled = true;
      stopPoll();
    };
    // Session updates (websocket) arrive with every status change, so reload on them.
  }, [enabled, sessions]);
  return enabled ? ids : NONE;
}

