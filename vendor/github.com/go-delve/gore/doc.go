// This file is part of GoRE.
//
// Copyright (C) 2019-2021 GoRE Authors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

/*
Package gore is a library for analyzing Go binaries.

	Only little endian architectures are supported.

Go compiler

The library has functionality for guessing the compiler version
used. It searches the binary for the identifiable string left
by the compiler. It is not perfect, so functionality for assuming
a specific version is also provided. The version strings used are
identical to the identifiers. For example version "1.10.1" is
represented as "go1.10.1" and version "1.10" is represented as
"go1.10"

Function recovery

Function information is recovered from the pclntab. Information
that is recovered includes: function start and end location in
the text section, source file. The library also tries to estimate
the first and last line number in the source file for the function.
The methods recovered includes the receiver. All functions and
methods belong to a package. The library tries to classify the
type of package. If it is a standard library package, 3rd-party
package, or part of the main application. If it unable to classify
the package, it is classified as unknown.

Type recovery

The types in the binary are parsed from the "typelink" list. Not
all versions of Go are supported equally. Versions 1.7 and later
are fully supported. Versions 1.5 and 1.6 are partially supported.
Version prior to 1.5 are not supported at all at the moment.

Example code

Extract the main package, child packages, and sibling packages:

	f, err := gore.Open(fileStr)
	pkgs, err := f.GetPackages()

Extract all the types in the binary:

	f, err := gore.Open(fileStr)
	typs, err := f.GetTypes()

*/
package gore
