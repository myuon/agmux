// Rate limit rejected scenario
export const rateLimitRejectedLines = [
  {
    type: "user",
    message: {
      role: "user",
      content: "Please continue with the implementation.",
    },
  },
  {
    type: "rate_limit_event",
    rate_limit_info: {
      status: "rejected",
      resetsAt: Math.floor(Date.now() / 1000) + 3600,
      rateLimitType: "seven_day",
      overageStatus: "rejected",
      overageDisabledReason: "out_of_credits",
      isUsingOverage: false,
    },
  },
];
