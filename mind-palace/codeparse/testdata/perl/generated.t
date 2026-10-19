#!/usr/bin/perl
# A test with several statement packages, generated subs, a heredoc and a
# forward declaration: each package statement qualifies what follows it,
# up to the next one, though the grammar does not nest them.
use strict;
use warnings;
use Test::More tests => 3;

package Fixture::One;
sub build { return bless {}, shift }

package Fixture::Two;
our @ISA = ('Fixture::One');
sub build { my $self = Fixture::One::build(@_); $self->{two} = 1; $self }

package main;

for my $name (qw(alpha beta)) {
    no strict 'refs';
    *{"Fixture::One::$name"} = sub { return $name };
}

my $text = <<"END";
sub not_a_sub { 1 }
END

eval "sub from_string { 42 }";

ok(Fixture::Two->build->{two}, 'two builds');
is(Fixture::One->alpha, 'alpha', 'generated');
like($text, qr/not_a_sub/, 'heredoc is text');

sub later;
