import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { X } from "lucide-react";
import { api, type ChoiceCard } from "../../api/client";
import { useExperimental } from "../../hooks/useExperimental";
import { useWebSocket } from "../../hooks/useWebSocket";
import { Chip } from "../ui/Chip";
import { spliceChoiceCards } from "../../hooks/useChoiceCards";

// Choice cards (experimental "choice_cards"): buttons shown in the
// conversation at the position where the card was offered. They are stored in
// the DB, not in the stream: ChoiceCardsStream splices a synthetic stream
// entry at card.streamOffset and the stream renderer shows <ChoiceCardView/>.

interface ChoiceCardsContextValue {
  cards: Map<string, ChoiceCard>;
  update: (card: ChoiceCard) => void;
}

const ChoiceCardsContext = createContext<ChoiceCardsContextValue | null>(null);

function ChoiceCardsEnabled({ sessionId, lines, historyStart, children }: {
  sessionId: string | undefined;
  lines: unknown[];
  historyStart: number;
  children: (lines: unknown[]) => ReactNode;
}) {
  const [cards, setCards] = useState<ChoiceCard[]>([]);

  const reload = useCallback(() => {
    if (!sessionId) return;
    api.listChoiceCards(sessionId).then((r) => setCards(r.cards)).catch(() => {});
  }, [sessionId]);

  const update = useCallback((card: ChoiceCard) => {
    setCards((prev) => (prev.some((c) => c.id === card.id)
      ? prev.map((c) => (c.id === card.id ? card : c))
      : [...prev, card]));
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  const { connectionState } = useWebSocket(
    useCallback((msg: { type: string; data: unknown }) => {
      if (msg.type !== "choice_card" && msg.type !== "choice_card_updated") return;
      const data = msg.data as { sessionId: string; card: ChoiceCard };
      if (data.sessionId === sessionId) update(data.card);
    }, [sessionId, update]),
  );
  // Catch up on cards missed while the socket was down.
  useEffect(() => {
    if (connectionState === "connected") reload();
  }, [connectionState, reload]);

  const map = useMemo(() => new Map(cards.map((c) => [c.id, c])), [cards]);
  const ctx = useMemo(() => ({ cards: map, update }), [map, update]);
  const spliced = useMemo(() => spliceChoiceCards(lines, historyStart, cards), [lines, historyStart, cards]);

  return <ChoiceCardsContext.Provider value={ctx}>{children(spliced)}</ChoiceCardsContext.Provider>;
}

/**
 * Wraps the stream view: while choice_cards is on, passes lines with the
 * session's cards spliced in; while off, passes lines through untouched.
 */
export function ChoiceCardsStream(props: {
  sessionId: string | undefined;
  lines: unknown[];
  historyStart: number;
  children: (lines: unknown[]) => ReactNode;
}) {
  const enabled = useExperimental("choice_cards");
  if (!enabled) return <>{props.children(props.lines)}</>;
  return <ChoiceCardsEnabled key={props.sessionId} {...props} />;
}

export function ChoiceCardView({ cardId }: { cardId: string }) {
  const ctx = useContext(ChoiceCardsContext);
  const card = ctx?.cards.get(cardId);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (!ctx || !card || card.status === "dismissed") return null;

  if (card.status === "answered") {
    return (
      <div className="text-xs text-gray-500 border border-gray-200 rounded-lg bg-gray-50 px-3 py-2">
        <div className="text-gray-400">{card.prompt}</div>
        <div className="mt-0.5 font-medium text-gray-700">回答済み: {card.answer}</div>
      </div>
    );
  }

  const run = async (fn: () => Promise<ChoiceCard>) => {
    setBusy(true);
    setError(null);
    try {
      ctx.update(await fn());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border border-indigo-200 bg-indigo-50/60 rounded-lg px-3 py-2.5">
      <div className="flex items-start gap-2">
        <p className="flex-1 min-w-0 text-sm text-gray-800 whitespace-pre-wrap break-words">{card.prompt}</p>
        <button
          type="button"
          disabled={busy}
          onClick={() => run(() => api.dismissChoiceCard(card.id))}
          className="shrink-0 -mt-1 -mr-1 p-2 text-gray-400 hover:text-gray-600 rounded"
          title="閉じる"
          aria-label="閉じる"
        >
          <X className="w-4 h-4" />
        </button>
      </div>
      <div className="mt-2 flex flex-col sm:flex-row sm:flex-wrap gap-2">
        {card.choices.map((c) => (
          <button
            key={c.id}
            type="button"
            disabled={busy}
            onClick={() => run(() => api.answerChoiceCard(card.id, c.id))}
            className="min-h-[44px] sm:min-h-[36px] px-4 py-2 rounded-lg border border-indigo-300 bg-white text-sm font-medium text-indigo-700 hover:bg-indigo-100 active:bg-indigo-200 disabled:opacity-50 text-left sm:text-center break-words"
          >
            {c.label}
          </button>
        ))}
      </div>
      <p className="mt-1.5 text-[11px] text-gray-400">ボタンを押すか、そのまま返信してもかまいません。</p>
      {error && <p className="mt-1 text-xs text-red-600">{error}</p>}
    </div>
  );
}

export function ChoiceCardsBadge() {
  return (
    <span className="inline-flex shrink-0 whitespace-nowrap">
      <Chip color="blue">選択肢</Chip>
    </span>
  );
}
