#include <stdio.h>

/* ok is defined before the error. */
int ok(int x)
{
	return x + 1;
}

/* half is cut off mid-expression. */
int half(int y)
{
	return y * (2 + ;
}

struct after {
	int a;
};

int after_error(struct after *p)
{
	return ok(p->a);
}
