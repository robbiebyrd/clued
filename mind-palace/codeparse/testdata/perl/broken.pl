package Broken;
use strict;

sub ok_before { return 1 }

sub missing_brace {
    my ($x = @_;
    if ($x { return 2 }

sub ok_after { return helper(3) }

1;
