#include <windows.h>
#include <Winternl.h>

typedef struct THREAD_BASIC_INFORMATION
{
  NTSTATUS ExitStatus;
  PVOID TebBaseAddress;
  CLIENT_ID ClientId;
  ULONG_PTR AffinityMask;
  LONG Priority;
  LONG BasePriority;

} THREAD_BASIC_INFORMATION,*PTHREAD_BASIC_INFORMATION;

WINBOOL thread_basic_information(HANDLE h, PTHREAD_BASIC_INFORMATION addr);
