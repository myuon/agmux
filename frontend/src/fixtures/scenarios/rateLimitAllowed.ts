// Rate limit allowed with warning (five_hour / overageStatus: rejected)
export const rateLimitAllowedLines = [
  {
    type: "user",
    message: {
      role: "user",
      content: "Hello, can you help me with a task?",
    },
  },
  {
    type: "rate_limit_event",
    rate_limit_info: {
      status: "allowed",
      resetsAt: 1772892000,
      rateLimitType: "five_hour",
      overageStatus: "rejected",
      overageDisabledReason: "out_of_credits",
      isUsingOverage: false,
    },
  },
  {
    type: "assistant",
    message: {
      role: "assistant",
      content: [
        {
          type: "text",
          text: "Of course! I'd be happy to help. What would you like me to do?",
        },
      ],
    },
  },
];
