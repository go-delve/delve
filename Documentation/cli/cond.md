# Breakpoint conditions

Breakpoints have two conditions:

* The normal condition, which is specified using the command `cond <breakpoint> <expr>` (or by setting the Cond field when amending a breakpoint via the API), is any [expression](expr.md) which evaluates to true or false.
* The hitcount condition, which is specified `cond <breakpoint> -hitcount <operator> <number>` (or by setting the HitCond field when amending a breakpoint via the API), is a constraint on the number of times the breakpoint has been hit.

When a breakpoint location is encountered during the execution of the program, the debugger will:

* Evaluate the normal condition
* Stop if there is an error while evaluating the normal condition
* If the normal condition evaluates to true the hit count is incremented
* Evaluate the hitcount condition
* If the hitcount condition is also satisfied stop the execution at the breakpoint

