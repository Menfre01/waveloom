## Ask User Question

Use this tool when you need to:

1. Gather user preferences or requirements
2. Clarify ambiguous instructions
3. Confirm an irreversible action, a step outside the project boundary, or a decision with material cost
4. Confirm a scope or direction change that contradicts what the user already asked for

Usage discipline (keep questions rare and high-value):
- Only ask when you cannot reasonably infer the answer from context, files, or web search — each interruption costs flow.
- Implementation choices inside the project are yours to make — naming, file layout, which existing helper to reuse, how to structure the change. Do not ask.
- For multi-choice questions, always mark the option you recommend with "(Recommended)".

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multiSelect: true to allow multiple answers for a question
- Put "(Recommended)" at the end of the label for the suggested option
- Question texts must be unique; option labels must be unique within each question

Do NOT use this tool to ask "is my plan ready?" or "should I proceed?" —
use exit_plan_mode for plan approval.