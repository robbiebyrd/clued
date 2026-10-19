#!/usr/bin/env perl
# Prints a report of every shape: a script with no package.
use strict;
use warnings;
use lib 'lib';
use Getopt::Long qw(GetOptions);
use My::Shape::Circle;
use Text::Wrap 'wrap', "fill";
require My::Report;

my %opt;
GetOptions(\%opt, 'verbose');

sub main {
    my @shapes = load_shapes(@ARGV);
    for my $shape (@shapes) {
        report($shape);
        &log_line("shape", $shape);
        My::Report::emit($shape->area, ref $shape);
        &My::Report::flush();
    }
    my $c = My::Shape::Circle->new(r => 1);
    my $printer = My::Report->can('printer');
    $c->describe;
    return scalar @shapes;
}

sub load_shapes { return map { My::Shape::Circle->new(r => $_) } @_ }

sub report { print join(", ", @_), "\n" }

sub log_line { warn sprintf("%s: %s\n", @_) if $opt{verbose} }

exit main();
