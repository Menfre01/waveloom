## Exit Plan Mode

### Before Using This Tool
- Write your plan to the plan file first (use write_file with the plan file path shown in [plan:start #xxxx])
- Ensure your plan is complete and unambiguous
- Resolve any open questions with ask_user_question BEFORE calling exit_plan_mode

### How This Tool Works
- This tool reads the plan from the file you wrote
- The user will see the plan content and approve or request changes
- If approved, you return to normal mode and can begin implementation
- If rejected, you stay in plan mode to revise the plan

Do NOT use ask_user_question to ask "is my plan ready?" or "should I proceed?" — 
that's exactly what this tool does.