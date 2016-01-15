#include "threads_windows.h"

typedef NTSTATUS (WINAPI *pNtQIT)(HANDLE, LONG, PVOID, ULONG, PULONG);

WINBOOL thread_basic_information(HANDLE h, THREAD_BASIC_INFORMATION* addr) {
	static pNtQIT NtQueryInformationThread = NULL;
	if(NtQueryInformationThread == NULL) {
		NtQueryInformationThread = (pNtQIT)GetProcAddress(GetModuleHandle("ntdll.dll"), "NtQueryInformationThread");
		if(NtQueryInformationThread == NULL) {
			return 0;
		}
	}

	NTSTATUS status = NtQueryInformationThread(h, ThreadBasicInformation, addr, 48, 0);
	return NT_SUCCESS(status);
}
