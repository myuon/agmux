import { GitPullRequest } from "lucide-react";

interface PullRequestBadgeProps {
  number: number;
  state: string;
  url: string;
}

export function PullRequestBadge({ number, state, url }: PullRequestBadgeProps) {
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className={`inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full ${
        state === "MERGED"
          ? "bg-purple-100 text-purple-700"
          : state === "OPEN"
            ? "bg-green-100 text-green-700"
            : "bg-gray-100 text-gray-600"
      }`}
    >
      <GitPullRequest className="w-3 h-3" />
      #{number}
    </a>
  );
}
