package My::Shape::Circle;

# A circle with a radius; the classic one-package-per-file module.

use strict;
use warnings;
use parent -norequire, 'My::Shape';
use base qw(Exporter);
use POSIX qw(floor);
use Scalar::Util 'blessed';
use List::Util ();
use constant PI => 3.14159;
use constant { E => 2.718, ZERO => 0 };
require Carp;
require "shapes/config.pl";

our @ISA = ('My::Base');
our $VERSION = '1.02';
our @EXPORT_OK = qw(area);
our ($_registry, %cache);
my $count = 0;

# Creates a new circle.
# Takes a radius.
sub new {
    my ($class, %args) = @_;
    my $self = bless { r => $args{r} }, $class;
    $count++;
    return $self;
}

=head2 area

Returns the area.

=cut

sub area :lvalue {
    my $self = shift;
    return PI * $self->{r} ** 2;
}

sub _scale($$) { return $_[0] * $_[1] }

# Defined into another package from here.
sub My::Shape::Registry::register { Carp::croak("no registry") unless @_ }

sub describe {
    my $self = shift;
    our $calls;
    my $area = $self->area();
    printf("%s\n", floor($area));
    return blessed($self) . ': ' . $self->SUPER::describe();
}

1;
__END__

=head1 NAME

My::Shape::Circle - a circle
