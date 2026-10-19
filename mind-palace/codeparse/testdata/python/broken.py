"""A module with syntax errors in the middle."""
import os


def good(x: int) -> int:
    """Doubles x."""
    return x * 2


def bad(x):
    total = = os.path.join(x)
    return total


class StillHere(Base):
    def method(self) -> Result:
        return good(1)


AFTER = 1


register(StillHere, 
